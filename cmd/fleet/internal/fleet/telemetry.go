package fleet

// ObserveAction retains facts already written by a verb; failure never changes
// the verb's outcome. The journal is telemetry, not authority.
func ObserveAction(kind string, row Rec) {
	if ReadOnly {
		return
	}
	if err := AppendJSONL(Path("actions.jsonl"), Rec{"at": Now(), "action": kind, "row": row}); err != nil {
		logError(Rec{"telemetry": "actions", "error": err.Error()})
	}
}

// ForgetTakeover removes a speculative replacement successfully unwound by the adapter,
// from the event row and from the takeovers still to be announced.
func ForgetTakeover(key string) {
	HookTakeovers = withoutKey(HookTakeovers, key)
	unannounced = withoutKey(unannounced, key)
}

func withoutKey(rs []Rec, key string) []Rec {
	kept := rs[:0]
	for _, r := range rs {
		if S(r, "key") != key {
			kept = append(kept, r)
		}
	}
	return kept
}
