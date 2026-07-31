package main

import _ "embed"

// defaultPolicyBytes is the single checked-in canary policy. The optional
// -policy path remains available for validated experiments and failure tests.
//
//go:embed policies/tier-aware-canary.json
var defaultPolicyBytes []byte
