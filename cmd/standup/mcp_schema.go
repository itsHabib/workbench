package main

import (
	"reflect"
	"sort"
	"strings"

	"github.com/itsHabib/workbench/cmd/standup/internal/standup"
)

type toolSpec struct {
	verb        string
	description string
	flags       []string
	properties  map[string]any
	required    []string
	readOnly    bool
}

func stringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description, "minLength": 1}
}

var toolSpecs = map[string]toolSpec{
	"standup_agenda":  {verb: "agenda", description: "Read current sources and save a new agenda. No Fleet writes. Returns source availability and the agenda id.", flags: []string{"--json"}, properties: map[string]any{}},
	"standup_new":     {verb: "new", description: "Create an empty saved plan against an agenda, optionally carrying a stale record forward without confirmation.", flags: []string{"--json"}, properties: map[string]any{"agenda": stringSchema("agenda id"), "from": stringSchema("optional previous record id")}, required: []string{"agenda"}},
	"standup_show":    {verb: "show", description: "Read the entire saved plan, its path, readback and plan_digest. Read this version back before confirmation.", flags: []string{"--json"}, properties: map[string]any{"record": stringSchema("record id")}, required: []string{"record"}, readOnly: true},
	"standup_draft":   {verb: "draft", description: "Replace ALL editable plan fields against the last plan_digest. Carry forward retained entries. Clears confirmation if changed; refuses execution history or invalid cards. No Fleet writes.", properties: map[string]any{"record": stringSchema("record id"), "expect": stringSchema("plan_digest from last show/new/draft"), "plan": schemaFor(reflect.TypeFor[standup.Draft]())}, required: []string{"record", "expect", "plan"}},
	"standup_prepare": {verb: "prepare", description: "Check the live plan before confirmation. Returns planned effects and runtime warnings, including unavailable launch. Writes nothing; does not confirm or authorize.", properties: map[string]any{"record": stringSchema("record id")}, required: []string{"record"}, readOnly: true},
	"standup_confirm": {verb: "confirm", description: "Relay the actual user's exact words ONLY after full readback. Never infer or invent confirmation. Checks phrase and the displayed plan_digest; does not execute Fleet actions. The caller is trusted for human attribution.", properties: map[string]any{"record": stringSchema("record id"), "expect": stringSchema("plan_digest read back to the user"), "phrase": stringSchema("actual user words, never a phrase invented by the agent"), "surface": map[string]any{"type": "string", "enum": []string{"text", "voice"}}}, required: []string{"record", "expect", "phrase", "surface"}},
	"standup_apply":   {verb: "apply", description: "Execute the confirmed plan at expect. May create remote branches, pool seats, dispatch work and send orders. Refuses unconfirmed, changed or stale plans. No force-stale override is exposed.", properties: map[string]any{"record": stringSchema("record id"), "expect": stringSchema("confirmed plan_digest")}, required: []string{"record", "expect"}},
	"standup_status":  {verb: "status", description: "Read apply receipts, scoped Fleet runtime observations and required completion receipts for this plan. Process activity is not task completion.", properties: map[string]any{"record": stringSchema("record id")}, required: []string{"record"}, readOnly: true},
}

func mcpTools() []any {
	names := make([]string, 0, len(toolSpecs))
	for name := range toolSpecs {
		names = append(names, name)
	}
	sort.Strings(names)
	out := []any{}
	for _, name := range names {
		s := toolSpecs[name]
		required := s.required
		if required == nil {
			required = []string{}
		}
		out = append(out, map[string]any{"name": name, "description": s.description, "inputSchema": map[string]any{"type": "object", "properties": s.properties, "required": required, "additionalProperties": false}, "annotations": map[string]any{"readOnlyHint": s.readOnly}})
	}
	return out
}

// Generate the editable plan schema from its typed decoder so nested fields cannot
// silently diverge between the advertised tool and the CLI's accepted input.
func schemaFor(t reflect.Type) map[string]any {
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Int:
		return map[string]any{"type": "integer"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": schemaFor(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object"}
	case reflect.Struct:
		return structSchema(t)
	}
	return map[string]any{}
}

func structSchema(t reflect.Type) map[string]any {
	properties := map[string]any{}
	required := []string{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		properties[name] = schemaFor(f.Type)
		if opts != "omitempty" {
			required = append(required, name)
		}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}
