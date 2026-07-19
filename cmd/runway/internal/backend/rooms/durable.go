package rooms

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/itsHabib/workbench/cmd/runway/internal/backend"
	"github.com/itsHabib/workbench/cmd/runway/internal/claim"
)

// CleanupDurable best-effort reaps a Rooms allocation after controller loss.
// A known room id goes through the Rooms CLI's identity-safe kill/reap path;
// an allocation observed only as a supervisor process is killed but remains
// uncertain because its Firecracker id was never durably observed.
func CleanupDurable(privateDir string) (backend.CleanupResult, error) {
	allocation, err := readAllocation(privateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return backend.CleanupResult{}, nil
		}
		return backend.CleanupResult{}, err
	}
	if allocation.RoomID == "" {
		allocation.RoomID = roomIDFromLifecycle(filepath.Join(privateDir, lifecycleFile))
	}
	if allocation.RoomID != "" {
		return killRoom(allocation.RoomID)
	}
	if allocation.PID <= 0 {
		return backend.CleanupResult{}, nil
	}
	id := fmt.Sprintf("pid:%d", allocation.PID)
	if allocation.StartTicks == 0 {
		return backend.CleanupResult{Uncertain: true, AllocationID: id}, nil
	}
	ticks, tickErr := claim.StartTicks(allocation.PID)
	if tickErr != nil || ticks != allocation.StartTicks {
		return backend.CleanupResult{Uncertain: true, AllocationID: id}, nil
	}
	_ = killDurableGroup(allocation.PGID)
	return backend.CleanupResult{Uncertain: true, AllocationID: id}, nil
}

func readAllocation(privateDir string) (Allocation, error) {
	data, err := os.ReadFile(filepath.Join(privateDir, backendFile))
	if err != nil {
		return Allocation{}, err
	}
	var allocation Allocation
	if err := json.Unmarshal(data, &allocation); err != nil {
		return Allocation{}, fmt.Errorf("rooms: decode backend.json: %w", err)
	}
	return allocation, nil
}

func roomIDFromLifecycle(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record lifecycleRecord
		if json.Unmarshal(scanner.Bytes(), &record) == nil && record.RoomID != "" {
			return record.RoomID
		}
	}
	return ""
}

func killRoom(roomID string) (backend.CleanupResult, error) {
	config, err := ConfigFromEnvironment()
	if err != nil {
		return backend.CleanupResult{Uncertain: true, AllocationID: roomID}, nil
	}
	_ = runDurableCommand(config, "kill", roomID, "--json")
	_ = runDurableCommand(config, "gc", roomID)
	out, err := outputDurableCommand(config, "ls", "--json")
	if err != nil || roomPresent(out, roomID) {
		return backend.CleanupResult{Uncertain: true, AllocationID: roomID}, nil
	}
	return backend.CleanupResult{}, nil
}

func runDurableCommand(config Config, args ...string) error {
	_, err := outputDurableCommand(config, args...)
	return err
}

func outputDurableCommand(config Config, args ...string) ([]byte, error) {
	argv := append([]string(nil), config.Prefix...)
	argv = append(argv, args...)
	cmd := exec.Command(config.Launcher, argv...)
	cmd.Env = roomsEnv(backend.PreparedRun{Env: os.Environ()})
	return cmd.Output()
}

func roomPresent(data []byte, roomID string) bool {
	var report struct {
		SchemaVersion int `json:"schema_version"`
		Rooms         []struct {
			ID string `json:"id"`
		} `json:"rooms"`
	}
	if json.Unmarshal(data, &report) != nil {
		return true
	}
	if report.SchemaVersion != 1 {
		return true
	}
	for _, room := range report.Rooms {
		if room.ID == roomID {
			return true
		}
	}
	return false
}
