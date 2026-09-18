package state

import (
	"fmt"
	"strings"

	"schedlab/errs"
)

type State string

const (
	None      State = ""
	Pending   State = "PENDING"
	Running   State = "RUNNING"
	Succeeded State = "SUCCEEDED"
	Failed    State = "FAILED"
	Cancelled State = "CANCELLED"
)

func All() []State { return []State{Pending, Running, Succeeded, Failed, Cancelled} }

func Parse(s string) (State, error) {
	u := strings.ToUpper(s)
	for _, st := range All() {
		if string(st) == u {
			return st, nil
		}
	}
	return "", fmt.Errorf("state: %q: %w", s, errs.ErrInvalid)
}

var legal = map[[2]State]bool{
	{None, Pending}:      true,
	{Pending, Running}:   true,
	{Pending, Cancelled}: true,
	{Running, Succeeded}: true,
	{Running, Failed}:    true,
	{Running, Cancelled}: true,
	{Running, Pending}:   true,
}

func CanTransition(from, to State) bool { return legal[[2]State{from, to}] }

func Terminal(s State) bool {
	return s == Succeeded || s == Failed || s == Cancelled
}
