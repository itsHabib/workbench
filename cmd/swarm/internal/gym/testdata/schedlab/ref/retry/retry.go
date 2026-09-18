package retry

import (
	"fmt"
	"math"
	"time"

	"schedlab/errs"
)

type Policy struct {
	Base, Max      time.Duration
	Factor, Jitter float64
	MaxAttempts    int
}

var Default = Policy{Base: time.Second, Max: time.Minute, Factor: 2, Jitter: 0, MaxAttempts: 3}

func (p Policy) Validate() error {
	switch {
	case p.Base <= 0:
		return fmt.Errorf("retry: base %v: %w", p.Base, errs.ErrInvalid)
	case p.Max < p.Base:
		return fmt.Errorf("retry: max %v below base: %w", p.Max, errs.ErrInvalid)
	case p.Factor < 1:
		return fmt.Errorf("retry: factor %v: %w", p.Factor, errs.ErrInvalid)
	case p.Jitter < 0 || p.Jitter > 1:
		return fmt.Errorf("retry: jitter %v: %w", p.Jitter, errs.ErrInvalid)
	case p.MaxAttempts < 1:
		return fmt.Errorf("retry: max attempts %d: %w", p.MaxAttempts, errs.ErrInvalid)
	}
	return nil
}

func (p Policy) Retry(attempt int) bool {
	return attempt >= 1 && attempt < p.MaxAttempts
}

func (p Policy) Delay(attempt int, rnd func() float64) (time.Duration, error) {
	if err := p.Validate(); err != nil {
		return 0, err
	}
	if attempt < 1 {
		return 0, fmt.Errorf("retry: attempt %d: %w", attempt, errs.ErrInvalid)
	}
	if !p.Retry(attempt) {
		return 0, fmt.Errorf("retry: attempt %d of %d: %w", attempt, p.MaxAttempts, errs.ErrState)
	}
	raw := math.Min(float64(p.Max), float64(p.Base)*math.Pow(p.Factor, float64(attempt-1)))
	u := 0.5
	if rnd != nil {
		u = rnd()
	}
	return time.Duration(raw * (1 + p.Jitter*(2*u-1))), nil
}
