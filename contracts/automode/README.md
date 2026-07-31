# Auto-mode contracts

`AutoDecisionV1` is the provider-neutral decision artifact shared by
deterministic policy and lifecycle adapters. JSON Schema describes its
portable structure; every consumer must also apply the semantic contract laws
implemented by `ValidateDecision` and `ValidateAuditEvent`.

## Action digest

`action_digest` is SHA-256 over this exact byte sequence:

1. the ASCII domain separator `workbench.automode.inputs.v1`;
2. the action envelope and operation as framed strings;
3. the parameter count followed by parameter object entries sorted by key;
4. the observable count followed by observable object entries sorted by key.

A framed string is its UTF-8 byte length encoded as an unsigned LEB128 integer,
followed by its UTF-8 bytes. Each value entry is its framed key, one byte for
`redacted` (`0x00` false, `0x01` true), and its framed value. Counts use the
same unsigned LEB128 encoding. JSON object keys make names structurally unique.

This framing is deliberately independent of JSON object-member order,
whitespace, escaping, and any language's serializer. Golden byte and digest
vectors in `decision_test.go` pin the algorithm.

## Schema bundle

Both embedded schemas are self-contained. The audit schema carries a tested
copy of the decision definitions it references, so a Draft 2020-12 validator
does not need network access or a separately registered schema.

Named values are JSON objects keyed by normalized names, so ordinary Draft
2020-12 validators enforce both name syntax and uniqueness without an extension.
