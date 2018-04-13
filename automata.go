// Copyright 2018 University of Glasgow.
// Use of this source code is governed by an Apache
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"log"
	"sync"
)

const (
	stateClosed State = iota
	stateOpening
	stateOpened
	stateBinding
	stateBindError
	stateListening
	stateProcessingMessage
	stateMessageError
)

const (
	eventOpen Event = iota + 100
	eventClose
	eventBind
	eventSuccess
	eventError
	eventUnderLimit
	eventOverLimit
	eventChannelExpired
)

// Automata is a simple Finite State Machine (FSM). We can assign a callback function
// that will be invoked when FSM reaches a particular state. We can also raise an event
// to trigger a transition from one to another state.
type Automata struct {
	sync.RWMutex
	current     State
	transitions transitions
	callbacks   callbacks
}

// State represents a state of the automata
type State int

// Event represents an event of the automata
type Event int

// Transition is a state transition from state `Src` to state `Dest` when event `Event`
// raised.
type Transition struct {
	Src   State
	Event Event
	Dest  State
}

type transitions map[State]map[Event]State

// Callback is a function that will be invoked when Automata reaches a particular state.
type Callback func(data []interface{})

type callbacks map[State]Callback

// NewAutomata returns an instance of Automata.
func NewAutomata(current State, ts []Transition, callbacks callbacks) *Automata {
	fsm := &Automata{
		current:     current,
		transitions: make(transitions),
		callbacks:   callbacks,
	}
	for i := range ts {
		if _, ok := fsm.transitions[ts[i].Src]; !ok {
			fsm.transitions[ts[i].Src] = make(map[Event]State, 0)
		}
		fsm.transitions[ts[i].Src][ts[i].Event] = ts[i].Dest
	}
	return fsm
}

// Current returns the current state of Automata.
func (a *Automata) Current() State {
	a.RLock()
	defer a.RUnlock()
	return a.current
}

// Event triggers a transition of Automata from one to another state.
// Event returns an error when it cannot made the transition, for example:
// there is no available transition.
func (a *Automata) Event(event Event, data ...interface{}) error {
	var (
		dest State
		ok   bool
		cb   Callback
	)
	if dest, ok = a.transitions[a.current][event]; ok {
		a.Lock()
		log.Println("event", event.String(), "transition from",
			a.current.String(), "to", dest.String())
		a.current = dest
		a.Unlock()
		if cb, ok = a.callbacks[a.current]; ok {
			cb(data)
		}
		return nil
	}
	return fmt.Errorf("state %s does not have transition for event %s",
		a.current.String(), event.String())
}

func (s State) String() string {
	switch s {
	case stateClosed:
		return "closed"
	case stateOpening:
		return "opening"
	case stateOpened:
		return "opened"
	case stateBinding:
		return "binding"
	case stateBindError:
		return "bindError"
	case stateListening:
		return "listening"
	case stateProcessingMessage:
		return "processingMessage"
	case stateMessageError:
		return "messageError"
	}
	return "undefined"
}

func (e Event) String() string {
	switch e {
	case eventBind:
		return "bind"
	case eventChannelExpired:
		return "channelExpired"
	case eventClose:
		return "close"
	case eventError:
		return "error"
	case eventOpen:
		return "open"
	case eventOverLimit:
		return "overLimit"
	case eventSuccess:
		return "success"
	case eventUnderLimit:
		return "underLimit"
	}
	return "undefined"
}
