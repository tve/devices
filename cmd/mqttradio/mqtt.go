// Copyright (c) 2016 by Thorsten von Eicken, see LICENSE file for details

package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/eclipse/paho.mqtt.golang"
)

// Message describes an MQTT message with a topic and a JSON encoded payload. It is used here to
// isolate the GW code from the crazyness of the paho mqtt client and also to provide a generic
// type for passing messages or packets around.
type Message struct {
	Topic   string      // MQTT topic or, when publishing, topic suffix
	Payload interface{} // MQTT payload, should always be a JSON hash
}

// mq is a handle onto a MQTT broker connection.
type mq struct {
	conn     mqtt.Client          // broker connection
	subHooks []subHook            // subscription hooks
	dedupMu  sync.Mutex           // protects dedup
	dedup    map[uint64]time.Time // de-dup of messages we sent
}

// subHook is a subscription hook, that is, a hook to subscribe to messages internally so they
// get forwarded locally instead of traveling all the way to the broker and back. (Messages always
// get published to the broker, so the local routing is in addition, not in replacement.)
type subHook struct {
	topic  string        // topic that is being matched (exact match for now)
	ch     reflect.Value // channel for the subscription
	chElem reflect.Type  // type of the channel element
}

// newMQ connects to a broker and returns a new mq object. The connection is persistent, i.e.,
// re-establishes itself if there is a disconnect. Subscriptions also get renewed after a reconnect.
func newMQ(conf MqttConfig, debug LogPrintf) (*mq, error) {
	if debug != nil {
		debug("Configuring MQTT: %+v", conf)
	}
	//mqtt.DEBUG = log.New(os.Stderr, "", 0)
	mqtt.ERROR = log.New(os.Stderr, "", 0)
	opts := mqtt.NewClientOptions().
		AddBroker(fmt.Sprintf("tcp://%s:%d", conf.Host, conf.Port))
	opts.ClientID = "mqttradio"
	opts.Username = conf.User
	opts.Password = conf.Password

	mqConn := mqtt.NewClient(opts)
	if token := mqConn.Connect(); !token.WaitTimeout(10 * time.Second) {
		return nil, token.Error()
	}
	mq := &mq{conn: mqConn, dedup: make(map[uint64]time.Time)}
	go mq.gc()

	log.Printf("MQTT connected")
	return mq, nil
}

// gc is an endless loop that removes message de-duplication IDs that are older than a few
// minutes. These are evidently ones for which we don't have a subscription.
func (mq *mq) gc() {
	for {
		time.Sleep(time.Minute)
		mq.dedupMu.Lock()
		if mq.dedup == nil {
			return // mq must have been deallocated
		}
		tooOld := time.Now().Add(-10 * time.Minute)
		for h, t := range mq.dedup {
			if t.Before(tooOld) {
				delete(mq.dedup, h)
			}
		}
		mq.dedupMu.Unlock()
	}
}

// Publish publishes a message and handles immediate forwarding to any internal subscriptions.
func (mq *mq) Publish(topic string, payload interface{}) {
	// Internal subscription hooks. For now we assume that the types are reflect.Assignable.
	// Ideally we'd marshal and unmarshal via json if they're not in order to provide
	// exactly the same semantics as if we had gone via MQTT.
	payVal := reflect.Indirect(reflect.ValueOf(payload))
	for _, hook := range mq.subHooks {
		if topic == hook.topic {
			//log.Printf("PUB hook: %s", topic)
			chanMsg := reflect.Indirect(reflect.New(hook.chElem))
			chanMsg.FieldByName("Topic").SetString(topic)
			chanMsg.FieldByName("Payload").Set(payVal)
			hook.ch.Send(chanMsg)
		}
	}
	runtime.Gosched() // yield the CPU so any hooks can run

	// External MQTT publishing.
	jsonPayload, _ := json.Marshal(payload)
	mq.conn.Publish(topic, 1, false, jsonPayload)
	// Add message ID to de-dup hash with timestamp for GC.
	mq.dedupMu.Lock()
	hash := hashMessage(topic, string(jsonPayload))
	mq.dedup[hash] = time.Now()
	//log.Printf("Published %d to %s", hash, topic)
	mq.dedupMu.Unlock()
}

// Subscribe subscribes to an MQTT topic and ensures that internal forwarding occurs as well.
func (mq *mq) Subscribe(topic string, subChan interface{}) error {
	// A few sanity checks.
	chanType := reflect.TypeOf(subChan)
	if chanType.Kind() != reflect.Chan {
		panic("subChan must be a channel")
	}
	chanElemType := chanType.Elem()
	if chanElemType.Kind() != reflect.Struct {
		panic("subChan element must be struct")
	}
	chanValue := reflect.ValueOf(subChan)

	// Internal subscription hook.
	mq.subHooks = append(mq.subHooks, subHook{topic, chanValue, chanElemType})

	// MQTT subscription handler.
	handler := func(c mqtt.Client, m mqtt.Message) {
		// Check whether we sent it, in which case we already forwarded locally.
		payload := string(m.Payload())
		hash := hashMessage(topic, payload)
		//log.Printf("Sub got %d from %s", hash, topic)
		mq.dedupMu.Lock()
		_, dup := mq.dedup[hash]
		delete(mq.dedup, hash)
		mq.dedupMu.Unlock()
		if dup {
			return
		}

		msg := reflect.New(chanElemType)
		// This is a hack: instead of dealing with reflection ourselves we make
		// json.Unmarshal do the work.
		jsonMsg := fmt.Sprintf(`{"Topic":%q, "Payload":%s}`, m.Topic(), payload)
		if err := json.Unmarshal([]byte(jsonMsg), msg.Interface()); err != nil {
			log.Printf("cannot json decode payload for %s: %s", m.Topic(), err)
		} else {
			chanValue.Send(reflect.Indirect(msg))
		}
	}

	// Perform MQTT subscription.
	if token := mq.conn.Subscribe(topic, 1, handler); !token.WaitTimeout(2 * time.Second) {
		return token.Error()
	}

	return nil
}

func hashMessage(s ...string) uint64 {
	key := strings.Join(s, "ǂ")
	h := fnv.New64()
	h.Write([]byte(key))
	return h.Sum64()
}
