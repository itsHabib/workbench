package verify

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/itsHabib/workbench/contracts"
)

// Decider names who stands behind a judgment and through which channel their
// identity was established. It is the shared contract vocabulary, aliased here
// like Verdict and Producer: the type is shared, the rule that a judgment must
// carry one is a decision and stays in gate.
type Decider = contracts.Decider

// Decision methods — the channel vocabulary a Decider draws on.
const (
	// MethodCLIOperator is an operator running the gate CLI directly.
	MethodCLIOperator = contracts.MethodCLIOperator
	// MethodSlackInteractive is an operator tapping an Approve/Block button on a
	// Slack card, ingested through the escalate back-channel.
	MethodSlackInteractive = contracts.MethodSlackInteractive
	// MethodAuto prefixes a delegated auto-judgment: MethodAuto + provider.
	MethodAuto = contracts.MethodAuto
)

// ErrDeciderMissing fires when a judgment is composed without a decider. A
// judgment is the one verdict a person or a delegated provider AUTHORS rather
// than a verifier computes, so an unattributed one is a decision the log cannot
// attribute to anybody — the separation-of-duties gap this refusal closes.
//
// It bounds the WRITE path only. Reduce and every reader stay tolerant, because
// judgments recorded before the field existed must still explain and re-reduce;
// refusing to read them would trade one unanswerable question for a worse one.
var ErrDeciderMissing = errors.New("judgment_unattributed")

// knownMethods are the fixed channel names. A delegated judgment's method is
// MethodAuto + a provider name, checked separately, so the set stays closed
// without enumerating every provider here.
var knownMethods = []string{MethodCLIOperator, MethodSlackInteractive}

// NewDecider builds a decider from an identity and a channel, stamping the
// decider's own clock. The timestamp is deliberately distinct from the artifact
// envelope's append time: a decision taken on a phone and recorded once the
// back-channel drains has two different, both-true times, and collapsing them
// loses the one an auditor actually asks about.
func NewDecider(who, method string, now func() time.Time) (Decider, error) {
	d := Decider{
		Who:    strings.TrimSpace(who),
		Method: strings.TrimSpace(method),
		At:     now().UTC().Format(time.RFC3339),
	}
	if err := ValidateDecider(d); err != nil {
		return Decider{}, err
	}
	return d, nil
}

// ValidateDecider refuses an identity or channel gate cannot record honestly:
// an empty who names nobody, and an unknown method claims a channel gate has no
// vocabulary for. It does NOT authenticate — gate cannot, and pretending
// otherwise would be the more dangerous error. The method records how the
// decision ARRIVED, and the transport that names it is trusted to say so.
func ValidateDecider(d Decider) error {
	if strings.TrimSpace(d.Who) == "" {
		return fmt.Errorf("%w: who is required — a judgment must name its decider", ErrDeciderMissing)
	}
	if d.At == "" {
		return fmt.Errorf("%w: at is required — a decision must carry the decider's clock", ErrDeciderMissing)
	}
	if _, err := time.Parse(time.RFC3339, d.At); err != nil {
		return fmt.Errorf("%w: at %q is not RFC 3339: %v", ErrDeciderMissing, d.At, err)
	}
	if !knownMethod(d.Method) {
		return fmt.Errorf("%w: method %q is not one of %s or %s<provider>",
			ErrDeciderMissing, d.Method, strings.Join(knownMethods, ", "), MethodAuto)
	}
	return nil
}

// knownMethod reports whether method names a channel gate has vocabulary for.
// An auto-judgment's method is MethodAuto + the provider that produced it, and
// the provider set is validated where a provider is chosen, not here.
func knownMethod(method string) bool {
	for _, m := range knownMethods {
		if method == m {
			return true
		}
	}
	return strings.HasPrefix(method, MethodAuto) && len(method) > len(MethodAuto)
}

// RequireDecider is the write-path gate: a judgment-class verdict about to be
// appended must name its decider. Non-judgment verdicts pass untouched — a
// deterministic verifier has no decider, and demanding one would be noise.
func RequireDecider(v Verdict) error {
	if v.Producer.Class != ClassJudgment {
		return nil
	}
	if v.Decider == nil {
		return fmt.Errorf("%w: a %s verdict must name who decided and how", ErrDeciderMissing, ClassJudgment)
	}
	return ValidateDecider(*v.Decider)
}

// Attribution renders a decider for a human reader, and names the absence
// plainly when there is none. "unattributed" is the honest word for a judgment
// recorded before the binding existed: the reader must be able to tell "nobody
// is named" from "the name was not shown", and a blank line tells neither.
func Attribution(v Verdict) string {
	if v.Producer.Class != ClassJudgment {
		return ""
	}
	if v.Decider == nil {
		return "unattributed"
	}
	return fmt.Sprintf("%s via %s at %s", v.Decider.Who, v.Decider.Method, v.Decider.At)
}
