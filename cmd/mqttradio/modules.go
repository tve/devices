// Copyright (c) 2016 by Thorsten von Eicken, see LICENSE file for details

package main

import (
	"errors"
	"fmt"
	"reflect"
)

// module is a static descriptor for a message/packet processing module. A module
// gets instantiated by calling hookModule().
//
// The handler function of a module gets called with a packet as argument and
// a publishing function it can use to publish a processed packet (or multiple).
// In addition, it receives a debug logging function.
//
// The handler gets called using reflection in order to allow its definition to be
// strongly typed to the messages received. For this to work, its first argument must
// be a pointer to a struct that has a Topic string field and a Payload struct field.
// The Payload struct field can then describe the payload.
// See the RawRxMessage and RawTxMessage structs in raw.go for examples.
type module struct {
	name    string      // name of the module, needs to be used in the config
	handler interface{} // func(m *msgType, pub pubFunc, debug LogPrintf)
}

// pubFunc is the publishing function passed into a runner. The payload must
// consist of a pointer to a struct that can be marshaled to JSON (typ it
// needs to contain json tags).
type pubFunc func(topicSuffix string, payload interface{})

// modules is a global registry of modules that can be instantiated via config.
var modules map[string]module

// RegisterModule adds a module to the global registry. It is typically used in init() functions.
func RegisterModule(m module) {
	if modules == nil {
		modules = make(map[string]module)
	}
	modules[m.name] = m
}

// hookModule instantiates a module by launching a goroutine for the subscription and
// by providing a publishing function.
func hookModule(mc ModuleConfig, mq *mq, debug LogPrintf) error {
	debug("Hooking module %s (%s -> %s)", mc.Name, mc.Sub, mc.Pub)
	m, ok := modules[mc.Name]
	if !ok {
		return fmt.Errorf("module %s not found", m)
	}

	// Derive the type of the subscription message. This will panic if the runner doesn't have
	// an appropriate type, which is OK for now.
	handler := reflect.ValueOf(m.handler)
	handlerType := handler.Type()
	if handlerType.Kind() != reflect.Func || handlerType.NumIn() != 3 {
		return errors.New("module handler is not a function with 3 arguments")
	}
	msgType := handlerType.In(0)
	if msgType.Kind() != reflect.Ptr || msgType.Elem().Kind() != reflect.Struct {
		return errors.New("first arg of module handler is not a pointer to a struct")
	}

	// Create a publish function.
	pubFun := func(topicSuffix string, payload interface{}) {
		mq.Publish(mc.Pub+topicSuffix, payload)
	}

	// Create subscription function.
	subFuncType := reflect.FuncOf([]reflect.Type{msgType}, nil, false)
	subFunc := reflect.MakeFunc(subFuncType, func(args []reflect.Value) []reflect.Value {
		handler.Call([]reflect.Value{args[0], reflect.ValueOf(pubFun), reflect.ValueOf(debug)})
		return nil
	})

	// Create the subscription, this will launch a goroutine.
	err := mq.Subscribe(mc.Sub, subFunc.Interface())
	return err
}
