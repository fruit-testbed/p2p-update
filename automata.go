package main

import (
	"fmt"
	"log"
	"sync"
)

// Automata is a simple Finite State Machine (FSM). We can assign a callback function
// that will be invoked when FSM reaches a particular state. We can also raise an event
// to trigger a transition from one to another state.
type Automata struct {
	sync.RWMutex
	current     int
	transitions transitions
	callbacks   callbacks
}

// Transition is a state transition from state `Src` to state `Dest` when event `Event`
// raised.
type Transition struct {
	Src   int
	Event int
	Dest  int
}

type transitions map[int]map[int]int

// Callback is a function that will be invoked when Automata reaches a particular state.
type Callback func(data []interface{})

type callbacks map[int]Callback

// NewAutomata returns an instance of Automata.
func NewAutomata(current int, ts []Transition, callbacks callbacks) *Automata {
	fsm := &Automata{
		current:     current,
		transitions: make(transitions),
		callbacks:   callbacks,
	}
	for i := range ts {
		if _, ok := fsm.transitions[ts[i].Src]; !ok {
			fsm.transitions[ts[i].Src] = make(map[int]int, 0)
		}
		fsm.transitions[ts[i].Src][ts[i].Event] = ts[i].Dest
	}
	return fsm
}

// Current returns the current state of Automata.
func (a *Automata) Current() int {
	a.RLock()
	defer a.RUnlock()
	return a.current
}

// Event triggers a transition of Automata from one to another state.
// Event returns an error when it cannot made the transition, for example:
// there is no available transition.
func (a *Automata) Event(event int, data ...interface{}) error {
	var (
		dest int
		ok   bool
		cb   Callback
	)
	if dest, ok = a.transitions[a.current][event]; ok {
		a.Lock()
		log.Println("event", event, "transition from", a.current, "to", dest)
		a.current = dest
		a.Unlock()
		if cb, ok = a.callbacks[a.current]; ok {
			cb(data)
		}
		return nil
	}
	return fmt.Errorf("state %d does not have transition for event %d", a.current, event)
}
