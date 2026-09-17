package poc

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/swarm/internal/swarm"
)

const usage = `swarm poc: the adversarial proof of concept

  swarm poc init DIR [--tasks N --packages P]      (no flags: the classic six tasks)
  swarm poc run --dir DIR --mode flat|tree [--model M] [--seats 4] [--only t1-...,t2-...]
               [--wall 20m] [--max-turns 80] [--lead-every 90s] [--operator-every 20s]
               [--watch-every 20s] [--kill-t2-after 2m] [--disk-fault-for 90s] [--disk-min 2G]
  swarm poc stats --dir DIR --run RUNDIR [--json]
  swarm poc compare --flat RUNDIR --tree RUNDIR
`

// Main is the poc subcommand entry. Returns the exit code.
func Main(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 3
	}
	var err error
	switch args[0] {
	case "init":
		err = initCmd(args[1:])
	case "run":
		err = runCmd(args[1:])
	case "stats":
		err = statsCmd(args[1:])
	case "compare":
		err = compareCmd(args[1:])
	default:
		fmt.Fprint(os.Stderr, usage)
		return 3
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 3
	}
	return 0
}

func initCmd(args []string) error {
	var dir string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		dir, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("poc init", flag.ContinueOnError)
	var spec Spec
	fs.IntVar(&spec.Tasks, "tasks", 0, "generated task count (0 = the classic six)")
	fs.IntVar(&spec.Packages, "packages", 4, "packages the generated tasks spread over")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if dir == "" && fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	if dir == "" {
		return fmt.Errorf("init needs a directory")
	}
	return Init(dir, spec)
}

func runCmd(args []string) error {
	fs := flag.NewFlagSet("poc run", flag.ContinueOnError)
	var o RunOptions
	var only, diskMin string
	fs.StringVar(&o.Dir, "dir", "", "sandbox directory")
	fs.StringVar(&o.Mode, "mode", "", "flat or tree")
	fs.StringVar(&o.Model, "model", "claude-sonnet-5", "model for every session")
	fs.IntVar(&o.Seats, "seats", 4, "concurrent builders")
	fs.StringVar(&only, "only", "", "comma-separated branches")
	fs.DurationVar(&o.BuilderWall, "wall", 20*time.Minute, "builder wall clock")
	fs.IntVar(&o.MaxTurns, "max-turns", 80, "builder max turns")
	fs.DurationVar(&o.LeadEvery, "lead-every", 90*time.Second, "tree mode lead tick")
	fs.DurationVar(&o.OperatorEvery, "operator-every", 20*time.Second, "operator sim tick")
	fs.DurationVar(&o.WatchEvery, "watch-every", 20*time.Second, "watcher tick")
	fs.DurationVar(&o.KillT2After, "kill-t2-after", 2*time.Minute, "fault B")
	fs.DurationVar(&o.DiskFaultFor, "disk-fault-for", 90*time.Second, "fault D")
	fs.StringVar(&diskMin, "disk-min", "2G", "admission disk floor")
	fs.StringVar(&o.Out, "out", "", "run directory")
	fs.StringVar(&o.FlatBin, "flat-bin", "", "swarm binary for builders (default this one)")
	fs.BoolVar(&o.Consolidate, "consolidate", false, "run a consolidator after the builders")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if o.Dir == "" {
		return fmt.Errorf("--dir is required")
	}
	if only != "" {
		o.Only = strings.Split(only, ",")
	}
	var err error
	if o.DiskMin, err = swarm.ParseBytes(diskMin); err != nil {
		return err
	}
	return Run(o)
}

func statsCmd(args []string) error {
	fs := flag.NewFlagSet("poc stats", flag.ContinueOnError)
	dir := fs.String("dir", "", "sandbox directory")
	run := fs.String("run", "", "run directory")
	jsonOut := fs.Bool("json", false, "json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" || *run == "" {
		return fmt.Errorf("--dir and --run are required")
	}
	sc, err := ScoreRun(*dir, *run)
	if err != nil {
		return err
	}
	_ = writeJSONFile(filepath.Join(*run, "score.json"), sc)
	_ = os.WriteFile(filepath.Join(*run, "SCORE.md"), []byte(sc.Markdown()), 0o644)
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(sc)
	}
	fmt.Print(sc.Markdown())
	return nil
}

func compareCmd(args []string) error {
	fs := flag.NewFlagSet("poc compare", flag.ContinueOnError)
	flatRun := fs.String("flat", "", "flat run directory")
	treeRun := fs.String("tree", "", "tree run directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	load := func(run string) (*Score, error) {
		var sc Score
		if err := readJSONFile(filepath.Join(run, "score.json"), &sc); err != nil {
			return nil, fmt.Errorf("%s: run `swarm poc stats` first (%w)", run, err)
		}
		return &sc, nil
	}
	f, err := load(*flatRun)
	if err != nil {
		return err
	}
	t, err := load(*treeRun)
	if err != nil {
		return err
	}
	fmt.Print(Compare(f, t))
	return nil
}
