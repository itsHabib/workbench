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

// ForgetTakeover removes a speculative replacement successfully unwound by the adapter.
func ForgetTakeover(key string) {
	kept := HookTakeovers[:0]
	for _, r := range HookTakeovers {
		if S(r, "key") != key {
			kept = append(kept, r)
		}
	}
	HookTakeovers = kept
}
