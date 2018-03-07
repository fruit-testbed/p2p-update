package main

import (
	"fmt"
	"log"
	"sync"
)

type Automata struct {
	sync.RWMutex
	current     int
	transitions transitions // src->(event->dest)
	callbacks   callbacks   // dest->callback
}

type Transition struct {
	src   int
	event int
	dest  int
}

type transitions map[int]map[int]int

type Callback func(data []interface{})

type callbacks map[int]Callback

func NewAutomata(current int, ts []Transition, callbacks callbacks) *Automata {
	fsm := &Automata{
		current:     current,
		transitions: make(transitions),
		callbacks:   callbacks,
	}
	for i := range ts {
		if _, ok := fsm.transitions[ts[i].src]; !ok {
			fsm.transitions[ts[i].src] = make(map[int]int, 0)
		}
		fsm.transitions[ts[i].src][ts[i].event] = ts[i].dest
	}
	return fsm
}

func (a *Automata) Current() int {
	a.RLock()
	defer a.RUnlock()
	return a.current
}

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
