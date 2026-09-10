package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

type mailCommand struct {
	Cwd string   `json:"cwd"`
	Cmd []string `json:"cmd"`
}

// DeliverMail reserves at most one launch attempt per message. Reservation precedes
// Start deliberately: an interrupted attempt cannot be retried automatically.
// Delivery is not an ack, nor evidence that the child read the message.
func DeliverMail() error {
	if fleet.ReadOnly {
		return nil
	}
	b, err := os.ReadFile(fleet.Path("deliver.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var commands map[string]mailCommand
	if err = json.Unmarshal(b, &commands); err != nil {
		return fmt.Errorf("deliver.json: %w", err)
	}
	grace := 10 * time.Second
	if value := os.Getenv("FLEET_MAIL_GRACE"); value != "" {
		grace, err = time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("FLEET_MAIL_GRACE %q: %w", value, err)
		}
		if grace < 0 {
			return fmt.Errorf("FLEET_MAIL_GRACE %q must be nonnegative", value)
		}
	}
	roles := make([]string, 0, len(commands))
	for role := range commands {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	var failures []string
	for _, role := range roles {
		if err := deliverRole(role, commands[role], grace); err != nil {
			failures = append(failures, role+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("mail delivery: %s", strings.Join(failures, "; "))
	}
	return nil
}

func deliverRole(role string, config mailCommand, grace time.Duration) error {
	if _, err := fleet.MailRoleTenant(role); err != nil {
		return err
	}
	if config.Cwd == "" || fleet.RoleOf(config.Cwd) != role || len(config.Cmd) == 0 || config.Cmd[0] == "" {
		return fmt.Errorf("deliver.json needs a cmd and cwd bound to %s", role)
	}
	rows, err := fleet.Mail(role, true)
	if err != nil {
		return err
	}
	var failures []string
	for _, r := range rows {
		if err := deliverMessage(role, fleet.S(r, "id"), config, grace); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}

func deliverMessage(role, id string, config mailCommand, grace time.Duration) error {
	return fleet.MailLock(func() error {
		r, err := fleet.ReadMail(role, id)
		if err != nil {
			return err
		}
		if r == nil || fleet.Has(r, "acked_at") || fleet.Has(r, "delivered_at") || fleet.Now()-fleet.F(r, "at") < grace.Seconds() {
			return nil
		}
		live, err := mailRoleLive(role)
		if err != nil || live {
			return err
		}
		return launchMail(role, config, r)
	})
}

func mailRoleLive(role string) (bool, error) {
	entries, err := os.ReadDir(fleet.Path("sessions"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		r := fleet.ReadJSON(filepath.Join(fleet.Path("sessions"), e.Name()))
		if r == nil || fleet.S(r, "session") == "" {
			return false, fmt.Errorf("session liveness unknown: %s", e.Name())
		}
		if !fleet.B(r, "ended") && fleet.S(r, "launch_dir") == "" && fleet.S(r, "cwd") == "" {
			return false, fmt.Errorf("session identity unknown: %s", e.Name())
		}
		resolved, _, _ := fleet.MailIdentity(r)
		if resolved != role {
			continue
		}
		if !fleet.B(r, "ended") && fleet.S(r, "pid_kind") == "harness" && fleet.F(r, "pid") <= 0 {
			return false, fmt.Errorf("session liveness unknown: %s", e.Name())
		}
		if !fleet.B(r, "ended") && fleet.S(r, "pid_kind") != "harness" && fleet.F(r, "last_event_at") <= 0 {
			return false, fmt.Errorf("session liveness unknown: %s", e.Name())
		}
		if fleet.SessionAlive(r) {
			return true, nil
		}
	}
	return false, nil
}

func launchMail(role string, config mailCommand, r fleet.Rec) error {
	now := fleet.Now()
	token := fmt.Sprintf("watch-%d-%d", os.Getpid(), time.Now().UnixNano())
	prompt := "Mail for " + role + ". Run fleet mail --unacked to read the bodies; acknowledge with fleet ack <id> after reading.\n" + strings.Join(fleet.MailSummary([]fleet.Rec{r}), "\n")
	args := make([]string, len(config.Cmd))
	for i, a := range config.Cmd {
		args[i] = strings.ReplaceAll(a, "{{prompt}}", prompt)
	}
	log, err := os.OpenFile(fleet.Path("watch", token+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer log.Close()
	r["delivered_at"], r["delivered_by"] = now, token
	if err := fleet.WriteJSON(fleet.MailPath(role, fleet.S(r, "id")), r); err != nil {
		return err
	}
	ids := []string{fleet.S(r, "id")}
	event := fleet.Rec{"at": now, "what": "mail-delivery-attempt", "role": role, "ids": ids, "delivered_by": token}
	if err := fleet.AppendJSONL(fleet.Path("watch", "observed.jsonl"), event); err != nil {
		return err
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = config.Cwd
	cmd.Stdout, cmd.Stderr = log, log
	detach(cmd)
	if err := cmd.Start(); err != nil {
		event["what"], event["error"] = "mail-delivery-failed", err.Error()
		if logErr := fleet.AppendJSONL(fleet.Path("watch", "observed.jsonl"), event); logErr != nil {
			return fmt.Errorf("%w; recording failure: %v", err, logErr)
		}
		return err
	}
	event["what"], event["pid"] = "mail-delivery-started", cmd.Process.Pid
	err = fleet.AppendJSONL(fleet.Path("watch", "observed.jsonl"), event)
	go func() { _ = cmd.Wait() }()
	return err
}
