package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"math"
	"math/rand"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"

	"github.com/fabxc/tindex"
)

var errUsage = errors.New("usage error")

func main() {
	if err := Main(os.Args[1:]...); err == errUsage {
		fmt.Fprintln(os.Stderr, usage())
		os.Exit(2)
	} else if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage() string {
	return strings.TrimLeft(`
tindex tool

Usage:

	tindex command [arguments...]

Available commands:

	help           print this help text
	bench-write    run write benchmarks against tindex

Use "tindex command -h" for usage information about the command.
`, "\n")
}

type command interface {
	run(...string) error
	usage() string
}

func Main(args ...string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errUsage
	}

	var cmd command

	switch args[0] {
	case "bench-write":
		cmd = &benchCmd{}
	default:
		return errUsage
	}

	err := cmd.run(args[1:]...)
	if err == errUsage {
		fmt.Fprintln(os.Stderr, cmd.usage())
	}
	return err
}

type benchCmd struct {
	fs *flag.FlagSet
}

type benchOptions struct {
	labelsTotal     int
	labelsAvgValues int
	labelsMinValues int
	labelsMaxValues int

	setsTotal     int
	setsAvgLabels int
	setsMinLabels int
	setsMaxLabels int

	setsBatchSize int

	CPUProfile   string
	MemProfile   string
	BlockProfile string
}

func (cmd *benchCmd) usage() string {
	cmd.fs.Usage()
	return ""
}

func (cmd *benchCmd) run(args ...string) error {
	fs := flag.NewFlagSet("", flag.ContinueOnError)
	opts := &benchOptions{}

	fs.IntVar(&opts.labelsTotal, "labels.total", 4000, "total number of distinct key/value labels")
	fs.IntVar(&opts.labelsAvgValues, "labels.avg-values", 10, "avg number of values per label key")
	fs.IntVar(&opts.labelsMinValues, "labels.min-values", 10, "minimum values per label key")
	fs.IntVar(&opts.labelsMaxValues, "labels.max-values", 1000, "maximum values per label key")
	fs.IntVar(&opts.setsTotal, "sets.total", 1000000, "total number of sets")
	fs.IntVar(&opts.setsAvgLabels, "sets.avg-labels", 6, "min number of labels per set")
	fs.IntVar(&opts.setsMinLabels, "sets.min-labels", 3, "min number of labels per set")
	fs.IntVar(&opts.setsMaxLabels, "sets.max-labels", 12, "max number of labels per set")
	fs.IntVar(&opts.setsBatchSize, "sets.batch-size", 5000, "batch size for writing new sets")

	fs.StringVar(&opts.CPUProfile, "cpuprofile", "", "")
	fs.StringVar(&opts.MemProfile, "memprofile", "", "")
	fs.StringVar(&opts.BlockProfile, "blockprofile", "", "")

	cmd.fs = fs

	if err := fs.Parse(args); err != nil {
		return err
	}

	fmt.Println(">> generating test data")

	lbls := opts.genLabels()
	sets := opts.genSets(lbls)

	dir, err := ioutil.TempDir("", "tindex-bench")
	if err != nil {
		return err
	}

	ix, err := tindex.Open(dir, nil)
	if err != nil {
		return err
	}

	fmt.Println(">> starting writes")
	start := time.Now()
	cmd.startProfiling(opts)

	remSets := sets[:]
	for len(remSets) > 0 {
		n := opts.setsBatchSize
		if n > len(remSets) {
			n = len(remSets)
		}

		_, err := ix.EnsureSets(remSets[:n]...)
		if err != nil {
			return err
		}

		remSets = remSets[n:]
	}

	cmd.stopProfiling()
	fmt.Println(" > completed in", time.Since(start))

	return nil
}

// Starts all profiles set on the opts.
func (cmd *benchCmd) startProfiling(opts *benchOptions) {
	var err error

	// Start CPU profiling.
	if opts.CPUProfile != "" {
		cpuprofile, err = os.Create(opts.CPUProfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bench: could not create cpu profile %q: %v\n", opts.CPUProfile, err)
			os.Exit(1)
		}
		pprof.StartCPUProfile(cpuprofile)
	}

	// Start memory profiling.
	if opts.MemProfile != "" {
		memprofile, err = os.Create(opts.MemProfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bench: could not create memory profile %q: %v\n", opts.MemProfile, err)
			os.Exit(1)
		}
		runtime.MemProfileRate = 4096
	}

	// Start fatal profiling.
	if opts.BlockProfile != "" {
		blockprofile, err = os.Create(opts.BlockProfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bench: could not create block profile %q: %v\n", opts.BlockProfile, err)
			os.Exit(1)
		}
		runtime.SetBlockProfileRate(1)
	}
}

// File handlers for the various profiles.
var cpuprofile, memprofile, blockprofile *os.File

// Stops all profiles.
func (cmd *benchCmd) stopProfiling() {
	if cpuprofile != nil {
		pprof.StopCPUProfile()
		cpuprofile.Close()
		cpuprofile = nil
	}

	if memprofile != nil {
		pprof.Lookup("heap").WriteTo(memprofile, 0)
		memprofile.Close()
		memprofile = nil
	}

	if blockprofile != nil {
		pprof.Lookup("block").WriteTo(blockprofile, 0)
		blockprofile.Close()
		blockprofile = nil
		runtime.SetBlockProfileRate(0)
	}
}

type labels map[string][]string

func (opts *benchOptions) genLabels() labels {
	res := labels{}
	i := 0

	for i < opts.labelsTotal {
		var vals []string
		for j := 0; j < opts.randNumValues() && i < opts.labelsTotal; j++ {
			vals = append(vals, opts.randValue())
		}
		res[opts.randName()] = vals
		i++
	}

	return res
}

func (opts *benchOptions) genSets(lbls labels) []tindex.Set {
	res := []tindex.Set{}

	lnames := []string{}
	for ln := range lbls {
		lnames = append(lnames, ln)
	}

	for i := 0; i < opts.setsTotal; i++ {
		s := tindex.Set{}
		for j := 0; j < opts.randNumLabels(); {
			ln := lnames[rand.Intn(len(lnames))]
			lv := lbls[ln][rand.Intn(len(lbls[ln]))]

			if _, ok := s[ln]; ok {
				continue
			}
			s[ln] = lv
			j++
		}
		res = append(res, s)
	}

	return res
}

func (opts *benchOptions) randNumValues() int {
	return randNormInt(
		opts.labelsAvgValues,
		(opts.labelsMaxValues-opts.labelsMinValues)*2,
		opts.labelsMinValues,
		opts.labelsMaxValues,
	)
}

func (opts *benchOptions) randNumLabels() int {
	return randNormInt(
		opts.setsAvgLabels,
		(opts.setsMaxLabels-opts.setsMinLabels)*2,
		opts.setsMinLabels,
		opts.setsMaxLabels,
	)
}

func (opts *benchOptions) randName() string {
	return randString(randNormInt(8, 10, 3, 18))
}

func (opts *benchOptions) randValue() string {
	return randString(randNormInt(8, 20, 3, 64))
}

func randNormInt(mean, stddev, min, max int) int {
	v := rand.NormFloat64() * float64(mean) * float64(stddev)
	v = math.Min(v, float64(max))
	v = math.Max(v, float64(min))
	return int(v)
}

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
const (
	letterIdxBits = 6                    // 6 bits to represent a letter index
	letterIdxMask = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
	letterIdxMax  = 63 / letterIdxBits   // # of letter indices fitting in 63 bits
)

var src = rand.NewSource(1)

func randString(n int) string {
	b := make([]byte, n)
	// A src.Int63() generates 63 random bits, enough for letterIdxMax characters!
	for i, cache, remain := n-1, src.Int63(), letterIdxMax; i >= 0; {
		if remain == 0 {
			cache, remain = src.Int63(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < len(letterBytes) {
			b[i] = letterBytes[idx]
			i--
		}
		cache >>= letterIdxBits
		remain--
	}

	return string(b)
}
