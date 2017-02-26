// Copyright (c) 2016 by Thorsten von Eicken, see LICENSE file for details

package main

import (
	"errors"
	"fmt"
	"reflect"

	_ "github.com/kidoman/embd/host/chip"
)

// module is a static descriptor for a message/packet processing module. A module
// gets instantiated by calling hookModule().
//
// The runner function of a module gets called as a goroutine and its arguments
// are the subscription channel and the publishing function for it to use.
// In addition, it receives a debug logging function.
//
// The runner gets called using reflection in order to allow its definition to be
// strongly typed to the messages received. For this to work, its first argument needs
// to be a channel of a struct that has a Topic string field and a Payload struct field.
// The Payload struct field can then describe the payload.
type module struct {
	name   string      // name of the module, needs to be used in the config
	runner interface{} // func(sub <-chan subChanType, pub pubFunc, debug LogPrintf)
	//runner reflect.Value // func(sub <-chan subChanType, pub pubFunc, debug LogPrintf)
	//subChanType reflect.Type  // must be struct{Topic string; Payload <any>}
}

// pubFunc is the publishing function passed into a runner.
type pubFunc func(payload interface{})

// modules is a global registry of modules that can be instantiated via config.
var modules map[string]module

// RegisterModule adds a module to the global registry. It is typically used in init() functions.
func RegisterModule(m module) {
	if modules == nil {
		modules = make(map[string]module)
	}
	modules[m.name] = m
}

// hookModule instantiates a module, hooking it to a subscription channel and a publishing channel.
func hookModule(mc ModuleConfig, mq *mq, debug LogPrintf) error {
	debug("Hooking module %s (%s -> %s)", mc.Name, mc.Sub, mc.Pub)
	m, ok := modules[mc.Name]
	if !ok {
		return fmt.Errorf("module %s not found", m)
	}

	// Derive the type of the subscription channel. This will panic if the runner doesn't have
	// an appropriate type, which is OK for now.
	runner := reflect.ValueOf(m.runner)
	runnerType := runner.Type()
	if runnerType.Kind() != reflect.Func || runnerType.NumIn() != 3 {
		return errors.New("module runner is not a function with 3 arguments")
	}
	subChanType := runnerType.In(0)
	if subChanType.Kind() != reflect.Chan {
		return errors.New("first arg of module runner is not a channel")
	}

	// Create the subscription channel with the appropriate concrete type. Then use it to
	// subscribe.
	subChan := reflect.MakeChan(subChanType, 10)
	if err := mq.Subscribe(mc.Sub, subChan.Interface()); err != nil {
		return err
	}

	// Now start the runner goroutine, passing it an appropriate publication function.
	pubFun := func(payload interface{}) { mq.Publish(mc.Pub, payload) }
	go func() {
		runner.Call([]reflect.Value{subChan, reflect.ValueOf(pubFun), reflect.ValueOf(debug)})
	}()
	return nil
}
