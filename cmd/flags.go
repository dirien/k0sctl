package cmd

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"time"

	"github.com/k0sproject/k0sctl/analytics"
	"github.com/k0sproject/k0sctl/cache"
	"github.com/k0sproject/k0sctl/integration/segment"
	"github.com/k0sproject/k0sctl/phase"
	"github.com/k0sproject/k0sctl/version"
	"github.com/k0sproject/rig"
	"github.com/k0sproject/rig/exec"
	"github.com/logrusorgru/aurora"
	"github.com/shiena/ansicolor"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
)

var (
	debugFlag = &cli.BoolFlag{
		Name:    "debug",
		Usage:   "Enable debug logging",
		Aliases: []string{"d"},
		EnvVars: []string{"DEBUG"},
	}

	traceFlag = &cli.BoolFlag{
		Name:    "trace",
		Usage:   "Enable trace logging",
		EnvVars: []string{"TRACE"},
		Hidden:  false,
	}

	redactFlag = &cli.BoolFlag{
		Name:  "no-redact",
		Usage: "Do not hide sensitive information in the output",
		Value: false,
	}

	configFlag = &cli.StringFlag{
		Name:      "config",
		Usage:     "Path to cluster config yaml. Use '-' to read from stdin.",
		Aliases:   []string{"c"},
		Value:     "k0sctl.yaml",
		TakesFile: true,
	}

	analyticsFlag = &cli.BoolFlag{
		Name:    "disable-telemetry",
		EnvVars: []string{"DISABLE_TELEMETRY"},
	}

	Colorize = aurora.NewAurora(false)
)

// actions can be used to chain action functions (for urfave/cli's Before, After, etc)
func actions(funcs ...func(*cli.Context) error) func(*cli.Context) error {
	return func(ctx *cli.Context) error {
		for _, f := range funcs {
			if err := f(ctx); err != nil {
				return err
			}
		}
		return nil
	}
}

// initConfig takes the config flag, does some magic and replaces the value with the file contents
func initConfig(ctx *cli.Context) error {
	f := ctx.String("config")
	if f == "" {
		return nil
	}

	file, err := configReader(f)
	if err != nil {
		return err
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return err
	}

	return ctx.Set("config", string(content))
}

func displayCopyright(ctx *cli.Context) error {
	fmt.Printf("k0sctl %s Copyright 2021, k0sctl authors.\n", version.Version)
	if !ctx.Bool("disable-telemetry") {
		fmt.Println("Anonymized telemetry of usage will be sent to the authors.")
	}
	fmt.Println("By continuing to use k0sctl you agree to these terms:")
	fmt.Println("https://k0sproject.io/licenses/eula")
	return nil
}

func initAnalytics(ctx *cli.Context) error {
	if ctx.Bool("disable-telemetry") {
		log.Tracef("disabling telemetry")
		return nil
	}

	if segment.WriteKey == "" {
		log.Tracef("segment write key not set, analytics disabled")
		return nil
	}

	client, err := segment.NewClient()
	if err != nil {
		return err
	}
	analytics.Client = client

	return nil
}

// initLogging initializes the logger
func initLogging(ctx *cli.Context) error {
	log.SetLevel(log.TraceLevel)
	log.SetOutput(io.Discard)
	initScreenLogger(logLevelFromCtx(ctx, log.InfoLevel))
	exec.DisableRedact = ctx.Bool("no-redact")
	rig.SetLogger(log.StandardLogger())
	return initFileLogger()
}

// initSilentLogging initializes the logger in silent mode
// TODO too similar to initLogging
func initSilentLogging(ctx *cli.Context) error {
	log.SetLevel(log.TraceLevel)
	log.SetOutput(io.Discard)
	exec.DisableRedact = ctx.Bool("no-redact")
	initScreenLogger(logLevelFromCtx(ctx, log.FatalLevel))
	rig.SetLogger(log.StandardLogger())
	return initFileLogger()
}

func logLevelFromCtx(ctx *cli.Context, defaultLevel log.Level) log.Level {
	if ctx.Bool("trace") {
		return log.TraceLevel
	} else if ctx.Bool("debug") {
		return log.DebugLevel
	} else {
		return defaultLevel
	}
}

func initScreenLogger(lvl log.Level) {
	log.AddHook(screenLoggerHook(lvl))
}

func initFileLogger() error {
	lf, err := LogFile()
	if err != nil {
		return err
	}
	log.AddHook(fileLoggerHook(lf))
	return nil
}

func LogFile() (io.Writer, error) {
	logDir := cache.Dir()
	if err := cache.EnsureDir(logDir); err != nil {
		return nil, fmt.Errorf("error while creating log directory %s: %s", logDir, err.Error())
	}

	fn := path.Join(logDir, "k0sctl.log")
	logFile, err := os.OpenFile(fn, os.O_RDWR|os.O_CREATE|os.O_APPEND|os.O_SYNC, 0600)
	if err != nil {
		return nil, fmt.Errorf("Failed to open log %s: %s", fn, err.Error())
	}

	_, _ = fmt.Fprintf(logFile, "time=\"%s\" level=info msg=\"###### New session ######\"\n", time.Now().Format(time.RFC822))

	return logFile, nil
}

func configReader(f string) (io.ReadCloser, error) {
	if f == "-" {
		stat, err := os.Stdin.Stat()
		if err != nil {
			return nil, fmt.Errorf("can't stat stdin: %s", err.Error())
		}
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			return os.Stdin, nil
		}
		return nil, fmt.Errorf("can't read stdin")
	}

	variants := []string{f}
	// add .yml to default value lookup
	if f == "k0sctl.yaml" {
		variants = append(variants, "k0sctl.yml")
	}

	for _, fn := range variants {
		if _, err := os.Stat(fn); err != nil {
			continue
		}

		fp, err := filepath.Abs(fn)
		if err != nil {
			return nil, err
		}
		file, err := os.Open(fp)
		if err != nil {
			return nil, err
		}

		return file, nil
	}

	return nil, fmt.Errorf("failed to locate configuration")
}

type loghook struct {
	Writer    io.Writer
	Formatter log.Formatter

	levels []log.Level
}

func (h *loghook) SetLevel(level log.Level) {
	h.levels = []log.Level{}
	for _, l := range log.AllLevels {
		if level >= l {
			h.levels = append(h.levels, l)
		}
	}
}

func (h *loghook) Levels() []log.Level {
	return h.levels
}

func (h *loghook) Fire(entry *log.Entry) error {
	line, err := h.Formatter.Format(entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to format log entry: %v", err)
		return err
	}
	_, err = h.Writer.Write(line)
	return err
}

func screenLoggerHook(lvl log.Level) *loghook {
	var forceColors bool
	var writer io.Writer
	if runtime.GOOS == "windows" {
		writer = ansicolor.NewAnsiColorWriter(os.Stdout)
		forceColors = true
	} else {
		writer = os.Stdout
		if fi, _ := os.Stdout.Stat(); (fi.Mode() & os.ModeCharDevice) != 0 {
			forceColors = true
		}
	}

	if forceColors {
		Colorize = aurora.NewAurora(true)
		phase.Colorize = Colorize
	}

	l := &loghook{
		Writer:    writer,
		Formatter: &log.TextFormatter{DisableTimestamp: lvl < log.DebugLevel, ForceColors: forceColors},
	}

	l.SetLevel(lvl)

	return l
}

func fileLoggerHook(logFile io.Writer) *loghook {
	l := &loghook{
		Formatter: &log.TextFormatter{
			FullTimestamp:          true,
			TimestampFormat:        time.RFC822,
			DisableLevelTruncation: true,
		},
		Writer: logFile,
	}

	l.SetLevel(log.DebugLevel)

	return l
}

func displayLogo(_ *cli.Context) error {
	fmt.Println(logo)
	return nil
}
