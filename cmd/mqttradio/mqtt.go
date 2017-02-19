// Copyright (c) 2016 by Thorsten von Eicken, see LICENSE file for details

package main

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/eclipse/paho.mqtt.golang"
)

// message describes an MQTT message. It is used here to isolate the GW code
// from the crazyness of the paho mqtt client.
type message struct {
	node    byte
	payload []byte
}

type mq struct {
	conn     mqtt.Client
	rx0, rx1 chan message // radio -> mqtt
	tx0, tx1 chan message // mqtt -> radio
}

func newMQ(host, prefix0, prefix1 string) (*mq, error) {
	//mqtt.DEBUG = log.New(os.Stderr, "", 0)
	mqtt.ERROR = log.New(os.Stderr, "", 0)
	opts := mqtt.NewClientOptions().
		AddBroker("tcp://" + host).
		SetClientID("mqttradio")

	mqConn := mqtt.NewClient(opts)
	if token := mqConn.Connect(); !token.WaitTimeout(10 * time.Second) {
		return nil, token.Error()
	}
	log.Printf("MQTT connected")

	// getNode returns the node id from the topic.
	getNode := func(topic string) byte {
		s := strings.Split(topic, "/")
		n, _ := strconv.Atoi(s[len(s)-1])
		return byte(n)
	}

	// Subscribe for radio 0.
	tx0 := make(chan message, 10)
	handler0 := func(c mqtt.Client, m mqtt.Message) {
		tx0 <- message{node: getNode(m.Topic()), payload: m.Payload()}
	}
	if token := mqConn.Subscribe(prefix0+"/tx/+", 1, handler0); !token.WaitTimeout(2 * time.Second) {
		return nil, token.Error()
	}

	// Subscribe for radio 1.
	tx1 := make(chan message, 10)
	handler1 := func(c mqtt.Client, m mqtt.Message) {
		tx1 <- message{node: getNode(m.Topic()), payload: m.Payload()}
	}
	if token := mqConn.Subscribe(prefix1+"/tx/+", 1, handler1); !token.WaitTimeout(2 * time.Second) {
		return nil, token.Error()
	}
	log.Printf("MQTT subscribed")

	// Rx goroutine for radio 0.
	rx0 := make(chan message, 10)
	go func() {
		for m := range rx0 {
			topic := prefix0 + "/rx/" + strconv.Itoa(int(m.node))
			mqConn.Publish(topic, 1, false, m.payload)
		}
	}()

	// Rx goroutine for radio 1.
	rx1 := make(chan message, 10)
	go func() {
		for m := range rx1 {
			topic := prefix1 + "/rx/" + strconv.Itoa(int(m.node))
			mqConn.Publish(topic, 1, false, m.payload)
		}
	}()

	return &mq{conn: mqConn, rx0: rx0, rx1: rx1, tx0: tx0, tx1: tx1}, nil
}

func (mq *mq) disconnect() {
	close(mq.tx0)
	close(mq.tx1)
	mq.conn.Disconnect(1)
}
