package main

import (
	"fmt"
	"log"
	"sync"
)

type automata struct {
	sync.Mutex
	current     int
	transitions transitions // src->(event->dest)
	callbacks   callbacks   // dest->callback
}

type transition struct {
	src   int
	event int
	dest  int
}

type transitions map[int]map[int]int

type callback func(data []interface{})

type callbacks map[int]callback

func NewAutomata(current int, ts []transition, callbacks callbacks) *automata {
	fsm := &automata{
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

func (a *automata) event(event int, data ...interface{}) error {
	var (
		dest int
		ok   bool
		cb   callback
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
