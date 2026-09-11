# Example roles

These cards demonstrate Fleet's role format. They are not required roles, a standard
organization chart, or a policy supplied by Fleet. An organization owns its role names,
responsibilities, relationships, permissions and evidence requirements.

The installer copies these examples to the installed `lanes/` directory so a new user
can try role setup immediately. Before real work, adapt them or point `FLEET_LANES`
at your own directory of manifests/cards. The installer refuses to overwrite changed
cards, so preserve your customized directory separately when updating the examples.

- `author`: an example task owner.
- `verifier`: an example independent checker.
- `supervisor`: an example coordinator.

Each directory contains a `manifest.json` and `card.md`. The manifest names its kind,
card path, command denies, required receipt kinds, produced receipt kind and slot count.
These examples add no denies or receipt requirements. Fleet reads the data; it does
not hard-code these role names or infer authority from the prose.
