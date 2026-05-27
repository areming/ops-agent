// Package safety decides whether a proposed action runs automatically or
// needs the user's confirmation. It combines two layers: hard
// command-pattern rules (which the model cannot override) and the
// model's own reversibility/risk self-assessment. The more cautious of
// the two wins.
package safety

// Decision is the gate's verdict for an action.
type Decision int

const (
	Allow   Decision = iota // run without asking
	Confirm                 // ask the user first
)

func (d Decision) String() string {
	if d == Allow {
		return "allow"
	}
	return "confirm"
}

// SelfEval is the model's own assessment, parsed from the tool call's
// arguments. Reversible is a pointer so "unset" differs from "false".
type SelfEval struct {
	Reversible *bool  `json:"reversible"`
	Risk       string `json:"risk"` // low | medium | high
}

// Action is what a tool is about to do. Display is the command string for
// run_command (so command rules apply) or a short summary like
// "write /etc/x" for other tools.
type Action struct {
	Display  string
	ReadOnly bool
	Eval     SelfEval
}

type Verdict struct {
	Decision   Decision
	Risk       string // low | medium | high
	Reversible bool
	Reason     string
	// Danger is true when the action matched a hard danger rule. Session
	// auto-approve modes (yolo) must never bypass these.
	Danger bool
}

// Classify applies, in order: (1) hard danger rules, (2) the model's
// self-assessment as an escalation-only signal, (3) read-only auto-allow,
// (4) confirm anything else that writes.
func Classify(a Action) Verdict {
	if label := matchDanger(a.Display); label != "" {
		return Verdict{Confirm, "high", false, "matches dangerous pattern: " + label, true}
	}

	// The model can escalate to Confirm but never downgrade a rule.
	if a.Eval.Risk == "high" || (a.Eval.Reversible != nil && !*a.Eval.Reversible) {
		return Verdict{Confirm, evalRisk(a.Eval), false, "model flagged the action as risky or irreversible", false}
	}

	if a.ReadOnly || isReadOnlyCommand(a.Display) {
		return Verdict{Allow, "low", true, "read-only", false}
	}

	return Verdict{Confirm, "medium", reversibleOrFalse(a.Eval), "write operation needs confirmation", false}
}

func evalRisk(e SelfEval) string {
	if e.Risk != "" {
		return e.Risk
	}
	return "high"
}

func reversibleOrFalse(e SelfEval) bool {
	return e.Reversible != nil && *e.Reversible
}
