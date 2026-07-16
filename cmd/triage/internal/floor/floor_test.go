package floor

import (
	"strings"
	"testing"
)

// classifyDiff is a test helper: parse raw diff text and classify.
func classifyDiff(raw string) Result {
	return Classify(ParseUnifiedDiff(strings.NewReader(raw)))
}

func TestFloor(t *testing.T) {
	cases := []struct {
		name string
		want Tier
		diff string
	}{
		{
			name: "docs only -> T0",
			want: T0,
			diff: "diff --git a/README.md b/README.md\n+++ b/README.md\n@@\n+a badge line\n",
		},
		{
			name: "tests only -> T0",
			want: T0,
			diff: "diff --git a/pkg/thing_test.go b/pkg/thing_test.go\n+++ b/pkg/thing_test.go\n@@\n+func TestX(t *testing.T){}\n",
		},
		{
			// editing an already-applied migration is schema drift — max risk however small.
			name: "edit to an existing migration file -> T3",
			want: T3,
			diff: "diff --git a/store/migrations/0010_add_col.sql b/store/migrations/0010_add_col.sql\n+++ b/store/migrations/0010_add_col.sql\n@@\n+ALTER TABLE runs ADD COLUMN provider TEXT;\n",
		},
		{
			// held-out ship#180: a NEW purely-additive migration is owner-review, not critical —
			// blind consensus rated all three additive held-out migrations T2 (HELDOUT-01).
			name: "new additive migration (ADD COLUMN) -> T2",
			want: T2,
			diff: "diff --git a/store/migrations/0012_cont.sql b/store/migrations/0012_cont.sql\nnew file mode 100644\n--- /dev/null\n+++ b/store/migrations/0012_cont.sql\n@@\n+-- persist continuation intent\n+ALTER TABLE driver_streams ADD COLUMN work_on_current_branch INTEGER;\n",
		},
		{
			// held-out ship#178: CREATE TABLE + indexes in a new migration file -> T2.
			name: "new additive migration (CREATE TABLE + INDEX) -> T2",
			want: T2,
			diff: "diff --git a/store/migrations/0013_esc.sql b/store/migrations/0013_esc.sql\nnew file mode 100644\n--- /dev/null\n+++ b/store/migrations/0013_esc.sql\n@@\n+CREATE TABLE escalations (\n+  id TEXT PRIMARY KEY,\n+  class TEXT NOT NULL\n+);\n+CREATE UNIQUE INDEX esc_dedup ON escalations (class) WHERE id IS NOT NULL;\n",
		},
		{
			// held-out roxiq#123: an index swap (CREATE INDEX + DROP INDEX) touches no rows -> T2.
			name: "new migration swapping indexes (incl. DROP INDEX) -> T2",
			want: T2,
			diff: "diff --git a/migrations/027_cover.up.sql b/migrations/027_cover.up.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/027_cover.up.sql\n@@\n+CREATE INDEX IF NOT EXISTS idx_cover\n+    ON results (division, total_time)\n+    WHERE run_1 IS NOT NULL;\n+DROP INDEX IF EXISTS idx_old;\n",
		},
		{
			name: "new migration with a backfill UPDATE -> T3 (destructive)",
			want: T3,
			diff: "diff --git a/migrations/028_backfill.sql b/migrations/028_backfill.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/028_backfill.sql\n@@\n+ALTER TABLE users ADD COLUMN tier TEXT;\n+UPDATE users SET tier = 'free' WHERE tier IS NULL;\n",
		},
		{
			// fail-closed: a statement outside the additive allowlist (trigger = executable code
			// attached to existing tables) keeps the migration critical.
			name: "new migration with an unrecognized statement -> T3 (fail-closed)",
			want: T3,
			diff: "diff --git a/migrations/029_trig.sql b/migrations/029_trig.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/029_trig.sql\n@@\n+CREATE TRIGGER audit AFTER UPDATE ON users BEGIN\n+  INSERT INTO audit_log VALUES (NEW.id);\n+END;\n",
		},
		{
			// a README inside migrations/ is docs, not a migration.
			name: "docs file in a migrations dir -> T0",
			want: T0,
			diff: "diff --git a/migrations/README.md b/migrations/README.md\n+++ b/migrations/README.md\n@@\n+how to write a migration\n",
		},
		{
			name: "auth path -> T3",
			want: T3,
			diff: "diff --git a/internal/auth/session.go b/internal/auth/session.go\n+++ b/internal/auth/session.go\n@@\n+func check(){}\n",
		},
		{
			name: "removed authz call in a non-auth path -> T3 (content signal)",
			want: T3,
			diff: "diff --git a/handlers/user.go b/handlers/user.go\n+++ b/handlers/user.go\n@@\n-	if err := authorize(ctx, user); err != nil { return err }\n+	// fast path\n",
		},
		{
			name: "control-plane edit -> T3 (looks like docs)",
			want: T3,
			diff: "diff --git a/RUBRIC.md b/RUBRIC.md\n+++ b/RUBRIC.md\n@@\n-| migration | T3 |\n+| migration | T1 |\n",
		},
		{
			name: "runtime dependency -> T2",
			want: T2,
			diff: "diff --git a/Cargo.toml b/Cargo.toml\n+++ b/Cargo.toml\n@@\n+serde_norway = \"0.9\"\n",
		},
		{
			name: "dev/test dependency -> T1 (Experiment 01 fix; was over-firing to T2)",
			want: T1,
			diff: "diff --git a/Cargo.toml b/Cargo.toml\n+++ b/Cargo.toml\n@@\n [dev-dependencies]\n+proptest = \"1.0\"\n",
		},
		{
			// held-out dossier#81: a dep added INTO an existing dev section shows the section
			// header only as a context line — the section, not the dep name, marks it dev.
			name: "unknown dep added into existing [dev-dependencies] section -> T1",
			want: T1,
			diff: "diff --git a/Cargo.toml b/Cargo.toml\n+++ b/Cargo.toml\n@@\n [dev-dependencies]\n tempfile = \"3\"\n+rmcp = { version = \"1\", features = [\"client\"] }\n+tokio = { version = \"1\", features = [\"process\", \"time\"] }\n",
		},
		{
			// held-out dossier#81: the lockfile churn of a dev-only manifest change is itself dev —
			// the lock has no dev marking, but the manifest in the same diff does.
			name: "lockfile churn from a dev-only manifest change -> T1 (inherited)",
			want: T1,
			diff: "diff --git a/Cargo.toml b/Cargo.toml\n+++ b/Cargo.toml\n@@\n [dev-dependencies]\n+rmcp = \"1\"\n" +
				"diff --git a/Cargo.lock b/Cargo.lock\n+++ b/Cargo.lock\n@@\n+[[package]]\n+name = \"rmcp\"\n+version = \"1.0.0\"\n",
		},
		{
			// fail-closed: a lockfile-only diff can't prove the change is dev — stays runtime.
			name: "lockfile-only diff -> T2 (no manifest to inherit dev from)",
			want: T2,
			diff: "diff --git a/Cargo.lock b/Cargo.lock\n+++ b/Cargo.lock\n@@\n+[[package]]\n+name = \"leftpad\"\n+version = \"0.9.1\"\n",
		},
		{
			// a manifest touching BOTH sections is runtime — the dev part doesn't launder the rest.
			name: "manifest change spanning dev and runtime sections -> T2",
			want: T2,
			diff: "diff --git a/Cargo.toml b/Cargo.toml\n+++ b/Cargo.toml\n@@\n [dependencies]\n+serde = \"1\"\n@@\n [dev-dependencies]\n+proptest = \"1.0\"\n",
		},
		{
			// fail-closed: past a hunk boundary the section is unknown — an unattributed dep line
			// must not ride a dev classification from an earlier hunk.
			name: "dep line in a hunk with no visible section header -> T2",
			want: T2,
			diff: "diff --git a/Cargo.toml b/Cargo.toml\n+++ b/Cargo.toml\n@@\n [dev-dependencies]\n tempfile = \"3\"\n@@\n+serde = \"1\"\n",
		},
		{
			name: "infra/CI workflow -> T2",
			want: T2,
			diff: "diff --git a/.github/workflows/claude.yml b/.github/workflows/claude.yml\n+++ b/.github/workflows/claude.yml\n@@\n+  ref: ${{ github.event.pull_request.head.sha }}\n",
		},
		{
			name: "source override -> T3",
			want: T3,
			diff: "diff --git a/.npmrc b/.npmrc\n+++ b/.npmrc\n@@\n+registry=http://evil.example\n",
		},
		{
			name: "credential handling in a non-auth code path -> T3 (Experiment 01: dossier#64)",
			want: T3,
			diff: "diff --git a/src/s3store.rs b/src/s3store.rs\n+++ b/src/s3store.rs\n@@\n+    let creds = StaticCredentials::new(access_key_id, secret_access_key);\n",
		},
		{
			name: "iptables isolation in a .sh script -> T2 (Experiment 01: rooms#42)",
			want: T2,
			diff: "diff --git a/scripts/net-setup.sh b/scripts/net-setup.sh\n+++ b/scripts/net-setup.sh\n@@\n+iptables -A FORWARD -s 10.0.0.0/8 -j DROP\n",
		},
		{
			name: "plain internal code change -> T1",
			want: T1,
			diff: "diff --git a/internal/thing/calc.go b/internal/thing/calc.go\n+++ b/internal/thing/calc.go\n@@\n+func add(a,b int) int { return a+b }\n",
		},
		{
			name: "large generated regeneration -> T0 (size inversion)",
			want: T0,
			diff: bigGenerated(),
		},
		{
			// dossier#80: a code file touched only in doc-comments is docs-grade — the credential
			// keyword lives in prose (`/// Secret access key ...`), not in handling code.
			name: "comment-only change in a code file -> T0 (no secret signal, no internal-change)",
			want: T0,
			diff: "diff --git a/src/s3store.rs b/src/s3store.rs\n+++ b/src/s3store.rs\n@@\n+    /// Secret access key paired with the access key id.\n",
		},
		{
			// dossier#77: a credential keyword in a test fixture is not production secret handling.
			name: "credential keyword in a test file -> T0 (fixture, not secret handling)",
			want: T0,
			diff: "diff --git a/tests/s3_gate.rs b/tests/s3_gate.rs\n+++ b/tests/s3_gate.rs\n@@\n+    let secret = std::env::var(\"AWS_SECRET_ACCESS_KEY\").unwrap();\n",
		},
		{
			// dossier#82: RUBRIC §5.2 concurrency/locking → T2; the floor lacked the signal.
			name: "concurrency/locking primitive -> T2",
			want: T2,
			diff: "diff --git a/src/server.rs b/src/server.rs\n+++ b/src/server.rs\n@@\n+    let write_lock: Arc<tokio::sync::Mutex<()>> = Default::default();\n",
		},
		{
			// rooms#31: a source file NAMED like an infra tool, changed with benign code, is not
			// infra config — the isolation content signal, not the filename, marks a real boundary.
			name: "benign change to an infra-named source file -> T1 not T2",
			want: T1,
			diff: "diff --git a/src/firecracker.rs b/src/firecracker.rs\n+++ b/src/firecracker.rs\n@@\n+fn parse_banner(s: &str) -> Version { todo!() }\n",
		},
		{
			// codex review of PR #3: `--` must NOT be a comment marker or it eats CLI flags,
			// killing the isolation signal on a line that starts with --cap-drop / --privileged.
			name: "isolation flag at line start -> T2 (-- is not a comment)",
			want: T2,
			diff: "diff --git a/scripts/run.sh b/scripts/run.sh\n+++ b/scripts/run.sh\n@@\n+--cap-drop=ALL \\\n",
		},
		{
			// codex review of PR #3: a hash comment without a trailing space is still a comment,
			// so the credential keyword in it must not fire the secret signal (the real code is T1).
			name: "hash comment without a space -> suppressed (secret keyword in # comment)",
			want: T1,
			diff: "diff --git a/config.py b/config.py\n+++ b/config.py\n@@\n+#secret_access_key rotation note\n+timeout = 30\n",
		},
		{
			// cursor review of PR #3: `*creds = ...` is a pointer deref, not a block comment — it
			// must reach the content signals (a credential assignment via deref is real handling).
			name: "pointer-deref line is not a comment -> secret signal fires (T3)",
			want: T3,
			diff: "diff --git a/src/store.rs b/src/store.rs\n+++ b/src/store.rs\n@@\n+    *creds = secret_access_key.to_owned();\n",
		},

		// --- adversarial regressions (break-the-floor Workflow, 2026-07-08) ---
		// The additive-migration and dev-dep relaxations opened fail-opens; each is pinned here.
		{
			// a destructive statement after ';' on an otherwise-additive line must be seen
			name: "same-line DROP after CREATE in a new migration -> T3",
			want: T3,
			diff: "diff --git a/migrations/0042_x.sql b/migrations/0042_x.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/0042_x.sql\n@@\n+CREATE TABLE audit (id int); DROP TABLE sessions;\n",
		},
		{
			name: "ADD COLUMN + backfill UPDATE on one line -> T3",
			want: T3,
			diff: "diff --git a/migrations/0043_x.sql b/migrations/0043_x.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/0043_x.sql\n@@\n+ALTER TABLE users ADD COLUMN status text; UPDATE users SET status = 'x';\n",
		},
		{
			// a multi-line CREATE TABLE then a `; TRUNCATE` continuation line
			name: "leading-semicolon TRUNCATE after multi-line CREATE -> T3",
			want: T3,
			diff: "diff --git a/migrations/0044_x.sql b/migrations/0044_x.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/0044_x.sql\n@@\n+CREATE TABLE sessions_v2 (\n+  id text primary key\n+)\n+; TRUNCATE sessions;\n",
		},
		{
			// block comments are not '--' — a DROP behind /* */ must still be graded
			name: "block-comment-prefixed DROP -> T3",
			want: T3,
			diff: "diff --git a/migrations/0046_x.sql b/migrations/0046_x.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/0046_x.sql\n@@\n+CREATE TABLE ff (name text);\n+/* legacy */ DROP TABLE payment_methods;\n",
		},
		{
			name: "ADD COLUMN with volatile function DEFAULT (row rewrite) -> T3",
			want: T3,
			diff: "diff --git a/migrations/0045_x.sql b/migrations/0045_x.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/0045_x.sql\n@@\n+ALTER TABLE orders ADD COLUMN token uuid NOT NULL DEFAULT gen_random_uuid();\n",
		},
		{
			name: "ADD COLUMN GENERATED ALWAYS STORED (row rewrite) -> T3",
			want: T3,
			diff: "diff --git a/migrations/0047_x.sql b/migrations/0047_x.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/0047_x.sql\n@@\n+ALTER TABLE users ADD COLUMN email_ci text GENERATED ALWAYS AS (lower(email)) STORED;\n",
		},
		{
			// a spoofed "new file mode" on an edit (real old path) must not earn additive-T2
			name: "new file mode with a real old path is an edit -> T3",
			want: T3,
			diff: "diff --git a/migrations/0003_x.sql b/migrations/0003_x.sql\nnew file mode 100644\n--- a/migrations/0003_x.sql\n+++ b/migrations/0003_x.sql\n@@\n+ALTER TABLE users ADD COLUMN email text;\n",
		},
		{
			// a +++ path override must not launder an auth file into docs-T0
			name: "+++ path override on an auth file keeps the auth signal -> T3",
			want: T3,
			diff: "diff --git a/internal/auth/session.go b/internal/auth/session.go\n--- a/internal/auth/session.go\n+++ b/docs/notes/session.md\n@@\n-\trequire_role(\"admin\")\n+\t// relaxed\n",
		},
		{
			// a rename to a benign path must not shed the old path's config-security signal
			name: "rename from cors.yaml keeps config-security -> T2",
			want: T2,
			diff: "diff --git a/config/cors.yaml b/config/routes.yaml\nsimilarity index 72%\nrename from config/cors.yaml\nrename to config/routes.yaml\n--- a/config/cors.yaml\n+++ b/config/routes.yaml\n@@\n-  allowed_origins: [\"https://app.example.com\"]\n+  allowed_origins: [\"*\"]\n",
		},
		{
			// [dependencies] header with a trailing comment still resets the section
			name: "trailing comment on [dependencies] header -> runtime T2",
			want: T2,
			diff: "diff --git a/Cargo.toml b/Cargo.toml\n+++ b/Cargo.toml\n@@\n [dev-dependencies]\n mockito = \"1.2\"\n [dependencies] # keep sorted\n serde = \"1.0\"\n+evil_rt = \"0.1\"\n",
		},
		{
			// a runtime dep named after a test framework must not launder to dev
			name: "runtime dep named jest in [dependencies] -> T2",
			want: T2,
			diff: "diff --git a/Cargo.toml b/Cargo.toml\n+++ b/Cargo.toml\n@@\n [dependencies]\n serde = \"1.0\"\n+jest = \"0.1\"\n",
		},
		{
			// a compact JSON runtime block added as one line must be seen as runtime
			name: "compact JSON dependencies block -> T2",
			want: T2,
			diff: "diff --git a/package.json b/package.json\n+++ b/package.json\n@@\n   \"devDependencies\": {\n+    \"eslint\": \"^8.0.0\",\n     \"jest\": \"^29.0.0\"\n   },\n+  \"dependencies\": { \"evil-runtime\": \"^1.0.0\" },\n",
		},
		{
			// a git-source dependency is a source override (RUBRIC §5.3) even in a dev section
			name: "git-source dependency -> T3 override",
			want: T3,
			diff: "diff --git a/Cargo.toml b/Cargo.toml\n+++ b/Cargo.toml\n@@\n [dev-dependencies]\n mockito = \"1.2\"\n+test-helpers = { git = \"https://github.com/evil/x\", branch = \"main\" }\n",
		},
		{
			// [dev-dependencies] buried in a multi-line string must not flip the section
			name: "section header inside a TOML docstring -> runtime T2",
			want: T2,
			diff: "diff --git a/Cargo.toml b/Cargo.toml\n+++ b/Cargo.toml\n@@\n [dependencies]\n serde = \"1.0\"\n help_text = \"\"\"\n test crates live under\n [dev-dependencies]\n \"\"\"\n+telemetry_exfil = \"0.1\"\n",
		},
		{
			// a lockfile repointing a package to an off-registry host is a source override:
			// the added off-canonical `resolved` fires dep-override T3.
			name: "lockfile repoint to an off-registry host -> T3 override",
			want: T3,
			diff: "diff --git a/package.json b/package.json\n+++ b/package.json\n@@\n   \"devDependencies\": {\n+    \"eslint\": \"^8.0.0\"\n   }\n" +
				"diff --git a/package-lock.json b/package-lock.json\n+++ b/package-lock.json\n@@\n     \"node_modules/left-pad\": {\n-      \"resolved\": \"https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz\"\n+      \"resolved\": \"https://evil.example.com/left-pad-1.3.0.tgz\"\n",
		},
		{
			// cursor add-only: a NEW package block with an off-registry source (no matching
			// removal) must not inherit dev — the added off-canonical source fires T3.
			name: "add-only lockfile block with an off-registry source -> T3",
			want: T3,
			diff: "diff --git a/Cargo.toml b/Cargo.toml\n+++ b/Cargo.toml\n@@\n [dev-dependencies]\n+mockito = \"1.2\"\n" +
				"diff --git a/Cargo.lock b/Cargo.lock\n+++ b/Cargo.lock\n@@\n+[[package]]\n+name = \"backdoor\"\n+version = \"1.0.0\"\n+source = \"registry+https://evil.example.com/index\"\n",
		},
		{
			// a legit dev-dep's transitive additions resolve to a canonical registry and
			// still inherit dev -> T1 (the off-registry check must not over-call on these).
			name: "add-only lockfile block from a canonical registry still inherits dev -> T1",
			want: T1,
			diff: "diff --git a/Cargo.toml b/Cargo.toml\n+++ b/Cargo.toml\n@@\n [dev-dependencies]\n+mockito = \"1.2\"\n" +
				"diff --git a/Cargo.lock b/Cargo.lock\n+++ b/Cargo.lock\n@@\n+[[package]]\n+name = \"mockito\"\n+version = \"1.2.0\"\n+source = \"registry+https://github.com/rust-lang/crates.io-index\"\n",
		},

		// --- PR #4 adversarial review (claude + cursor) regressions ---
		{
			// claude fail-open A: a `--` inside a SQL string on the closing line must NOT
			// destroy the `;` boundary and hide the following DROP.
			name: "-- inside a SQL string literal does not hide a following DROP -> T3",
			want: T3,
			diff: "diff --git a/migrations/030_x.sql b/migrations/030_x.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/030_x.sql\n@@\n+CREATE TABLE config (\n+  note TEXT DEFAULT 'see release docs -- v2');\n+DROP TABLE sessions;\n",
		},
		{
			// claude fail-open B / cursor: an unterminated `/*` inside a string must not
			// truncate away a following DROP; a real unterminated `/*` fails closed.
			name: "/* inside a SQL string literal does not truncate a following DROP -> T3",
			want: T3,
			diff: "diff --git a/migrations/031_x.sql b/migrations/031_x.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/031_x.sql\n@@\n+CREATE TABLE config (\n+  note TEXT DEFAULT '/* reserved for future use');\n+DROP TABLE sessions;\n",
		},
		{
			// a genuinely unterminated block comment (outside strings) fails closed
			name: "unterminated block comment in a new migration -> T3 (fail-closed)",
			want: T3,
			diff: "diff --git a/migrations/032_x.sql b/migrations/032_x.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/032_x.sql\n@@\n+CREATE TABLE ok (id int);\n+/* opened and never closed\n+DROP TABLE sessions;\n",
		},
		{
			// a legit additive migration with a '--' comment and a string still grades T2
			name: "additive migration with a line comment and a benign string -> T2",
			want: T2,
			diff: "diff --git a/migrations/033_x.sql b/migrations/033_x.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/033_x.sql\n@@\n+-- add a note column\n+ALTER TABLE t ADD COLUMN note TEXT DEFAULT 'n/a -- not applicable';\n",
		},
		{
			// cursor fail-open: inString carried across a hunk boundary skipped a runtime
			// dep in the next hunk while an earlier dev change set sawChange -> laundered T1.
			name: "string opened in a dev hunk does not launder a runtime dep in the next hunk -> T2",
			want: T2,
			diff: "diff --git a/Cargo.toml b/Cargo.toml\n+++ b/Cargo.toml\n@@\n [dev-dependencies]\n+mockito = \"1.2\"\n desc = \"\"\"\n opening a string here\n@@\n [dependencies]\n+evil_runtime = \"0.1\"\n",
		},
		{
			// a DROP that is genuinely INSIDE a dollar-quoted literal is inert string
			// content (never executed), so an additive CREATE TABLE with it as a default
			// is correctly T2 — the scanner now recognizes the dollar-quote.
			name: "DROP inside a dollar-quoted literal is inert -> T2",
			want: T2,
			diff: "diff --git a/migrations/034_x.sql b/migrations/034_x.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/034_x.sql\n@@\n+CREATE TABLE x (c text DEFAULT $$a; DROP TABLE y; --$$);\n",
		},
		{
			// CREATE FUNCTION is not in the additive allowlist, so a plpgsql body (the only
			// realistic place a dollar-quote appears) is T3 regardless of its contents.
			name: "CREATE FUNCTION with a plpgsql body -> T3 (non-additive)",
			want: T3,
			diff: "diff --git a/migrations/035_x.sql b/migrations/035_x.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/035_x.sql\n@@\n+CREATE FUNCTION f() RETURNS trigger AS $$\n+BEGIN DELETE FROM audit; RETURN NEW; END;\n+$$ LANGUAGE plpgsql;\n",
		},

		// --- PR #4 cycle-2 review (claude) regressions: Postgres dialect strings ---
		{
			// FAIL-OPEN C: a backslash-escaped quote in an E-string must not exit the string
			// early and let the following `--` eat the `;` boundary that hides a DROP.
			name: "E-string with backslash-escaped quote does not hide a DROP -> T3",
			want: T3,
			diff: "diff --git a/migrations/036_x.sql b/migrations/036_x.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/036_x.sql\n@@\n+CREATE TABLE config (\n+  note TEXT DEFAULT E'docs \\' -- v2');\n+DROP TABLE sessions;\n",
		},
		{
			// FAIL-OPEN D: a dollar-quoted default with an embedded ' must not enter
			// single-quote mode and swallow the closing $$, the ;, and a following DROP.
			name: "dollar-quoted default with an embedded quote does not swallow a DROP -> T3",
			want: T3,
			diff: "diff --git a/migrations/037_x.sql b/migrations/037_x.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/037_x.sql\n@@\n+ALTER TABLE users ADD COLUMN note TEXT DEFAULT $$value's info$$;\n+DROP TABLE sessions;\n",
		},
		{
			// a legit additive migration with a dollar-quoted default still grades T2
			name: "additive migration with a benign dollar-quoted default -> T2",
			want: T2,
			diff: "diff --git a/migrations/038_x.sql b/migrations/038_x.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/038_x.sql\n@@\n+ALTER TABLE t ADD COLUMN note TEXT DEFAULT $tag$plain text$tag$;\n",
		},
		{
			// finding E: a `\"\"\"` inside a TOML # comment must not toggle string state and
			// skip a following runtime [dependencies] addition.
			name: "triple-quote in a TOML comment does not launder a runtime dep -> T2",
			want: T2,
			diff: "diff --git a/Cargo.toml b/Cargo.toml\n+++ b/Cargo.toml\n@@\n [dev-dependencies]\n+mockito = \"1.2\"\n # See \"\"\"deprecated config\n [dependencies]\n+evil_runtime = \"0.1\"\n",
		},

		// --- PR #4 cycle-3 review (claude + codex) regressions ---
		{
			// FAIL-OPEN F: an identifier ending in e/E before a string must NOT enable
			// E-string backslash-escape mode (standard SQL: `\` is literal there).
			name: "identifier ending in e before a SQL string does not enable escape mode -> T3",
			want: T3,
			diff: "diff --git a/migrations/039_x.sql b/migrations/039_x.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/039_x.sql\n@@\n+ALTER TABLE t ADD COLUMN note text DEFAULT sequence'val\\';\n+DROP TABLE sessions;\n",
		},
		{
			// FAIL-OPEN G: a `#[...]` line is a comment in a manifest (not a Rust attribute),
			// so its `\"\"\"` must not toggle inString and skip a runtime dep.
			name: "#[...] TOML comment with triple-quote does not launder a runtime dep -> T2",
			want: T2,
			diff: "diff --git a/Cargo.toml b/Cargo.toml\n+++ b/Cargo.toml\n@@\n [dev-dependencies]\n+mockito = \"1.2\"\n #[this is a TOML comment with \"\"\"\n [dependencies]\n+evil_runtime = \"0.1\"\n",
		},
		{
			// codex: CREATE TABLE ... AS SELECT copies existing rows -> backfill, not additive.
			name: "CREATE TABLE AS SELECT (backfill) -> T3",
			want: T3,
			diff: "diff --git a/migrations/040_x.sql b/migrations/040_x.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/040_x.sql\n@@\n+CREATE TABLE snapshot AS SELECT * FROM users;\n",
		},
		{
			// codex: a parenthesized volatile DEFAULT rewrites every row just like the bare form.
			name: "ADD COLUMN with a parenthesized volatile DEFAULT -> T3",
			want: T3,
			diff: "diff --git a/migrations/041_x.sql b/migrations/041_x.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/041_x.sql\n@@\n+ALTER TABLE orders ADD COLUMN token uuid DEFAULT (gen_random_uuid());\n",
		},
		{
			// a plain constant DEFAULT in parens is NOT volatile — stays additive T2.
			name: "ADD COLUMN with a parenthesized constant DEFAULT -> T2",
			want: T2,
			diff: "diff --git a/migrations/042_x.sql b/migrations/042_x.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/042_x.sql\n@@\n+ALTER TABLE t ADD COLUMN n int DEFAULT (0);\n",
		},

		// --- PR #4 cycle-4 review (claude + codex) regressions ---
		{
			// GAP H: a multi-action ALTER TABLE whose additive prefix matched must not
			// hide a comma-joined DROP COLUMN.
			name: "multi-action ALTER TABLE hiding a DROP COLUMN -> T3",
			want: T3,
			diff: "diff --git a/migrations/043_x.sql b/migrations/043_x.sql\nnew file mode 100644\n--- /dev/null\n+++ b/migrations/043_x.sql\n@@\n+ALTER TABLE users ADD COLUMN archived_at timestamptz, DROP COLUMN password_hash;\n",
		},
		{
			// GAP I: Cargo table-form dep source (git on its own line under [dependencies.x])
			// is a source override -> T3.
			name: "table-form git dependency source -> T3 override",
			want: T3,
			diff: "diff --git a/Cargo.toml b/Cargo.toml\n+++ b/Cargo.toml\n@@\n [dependencies.evil-lib]\n+git = \"https://github.com/evil/x\"\n+branch = \"main\"\n",
		},
		{
			// GAP J: a top-level package.json field after the devDependencies `}` must not
			// inherit the dev section and launder a runtime config change to T1.
			name: "package.json field after devDependencies close does not inherit dev -> T2",
			want: T2,
			diff: "diff --git a/package.json b/package.json\n+++ b/package.json\n@@\n   \"devDependencies\": {\n+    \"eslint\": \"^8.0.0\"\n   },\n+  \"type\": \"module\"\n }\n",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyDiff(c.diff).Floor
			if got != c.want {
				t.Fatalf("floor = %s, want %s", got, c.want)
			}
		})
	}
}

// bigGenerated builds a 2000-line generated-client diff — huge but T0.
func bigGenerated() string {
	var b strings.Builder
	b.WriteString("diff --git a/api/client.gen.go b/api/client.gen.go\n+++ b/api/client.gen.go\n@@\n")
	for i := 0; i < 2000; i++ {
		b.WriteString("+func Generated" + string(rune('A'+i%26)) + "() {}\n")
	}
	return b.String()
}
