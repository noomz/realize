package core

import (
	"github.com/fsnotify/fsnotify"
	"github.com/oxequa/grace"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"os/exec"
	"bufio"
	"errors"
)

type Watch struct {
	Exts  []string `yaml:"exts,omitempty" json:"exts,omitempty"`
	Paths []string `yaml:"paths,omitempty" json:"paths,omitempty"`
}

type Ignore struct {
	Dot   bool     `yaml:"dot,omitempty" json:"dot,omitempty"`
	Exts  []string `yaml:"exts,omitempty" json:"exts,omitempty"`
	Paths []string `yaml:"paths,omitempty" json:"paths,omitempty"`
}

// Command fields. Path run from a custom path. Log display command output.
type Command struct {
	Log bool   `yaml:"log,omitempty" json:"log,omitempty"`
	Cmd string `yaml:"cmd,omitempty" json:"cmd,omitempty"`
	Dir string `yaml:"dir,omitempty" json:"dir,omitempty"`
}

// Response contains a command response
type Response struct {
	Cmd *Command
	Out string
	Err error
}

// Activity struct contains all data about a program.
type Activity struct {
	*Realize
	Watch       *Watch
	Ignore      *Ignore
	files       []string
	folders     []string
	Tasks       []interface{}
	TasksAfter  []interface{}
	TasksBefore []interface{}
}

// Sequence list of commands to exec in sequence
type Sequence struct {
	Commands []Command `yaml:"sequence,omitempty" json:"sequence,omitempty"`
}

// Parallel list of commands to exec in parallel
type Parallel struct {
	Commands []Command `yaml:"parallel,omitempty" json:"parallel,omitempty"`
}

// Walk file three
func walk(path string, watcher FileWatcher) error {
	return filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		watcher.Walk(path, true)
		return nil
	})
}

// Scan an activity and wait a change
func (a *Activity) Scan(wg *sync.WaitGroup) (e error) {
	var ltime time.Time
	var w sync.WaitGroup
	var reload chan bool
	var watcher FileWatcher
	defer func() {
		close(reload)
		watcher.Close()
		grace.Recover(&e)
		wg.Done()
	}()
	// new chan
	reload = make(chan bool)
	// new file watcher
	watcher, err := NewFileWatcher(a.Options.Legacy)
	if err != nil {
		panic(e)
	}

	w.Add(1)
	// indexing
	go func() {
		defer w.Done()
		for _, p := range a.Watch.Paths {
			abs, _ := filepath.Abs(p)
			glob, _ := filepath.Glob(abs)
			for _, g := range glob {
				if _, err := os.Stat(g); err == nil {
					if err = walk(g, watcher); err != nil {
						a.Options.Recovery.Push(Prefix("Indexing", Red), err)
					}
				}
			}
		}
	}()
	// run tasks before
	a.Reload(a.TasksBefore, reload)
	// wait indexing and before
	w.Wait()

	// run tasks list
	go a.Reload(a.Tasks, reload)
L:
	for {
		select {
		case event := <-watcher.Events():
			a.Options.Recovery.Push(Prefix("File Changed", Magenta), event.Name)
			if time.Now().Truncate(time.Second).After(ltime) {
				switch event.Op {
				case fsnotify.Remove:
					watcher.Remove(event.Name)
					if s, _ := a.Validate(event.Name, false); s && Ext(event.Name) != "" {
						// stop and restart
						close(reload)
						reload = make(chan bool)
						Record(Prefix("Removed", Magenta), event.Name)
						go a.Reload(a.Tasks, reload)
					}
				case fsnotify.Create, fsnotify.Write, fsnotify.Rename:
					if s, fi := a.Validate(event.Name, true); s {
						if fi.IsDir() {
							if err = walk(event.Name, watcher); err != nil {
								a.Options.Recovery.Push(Prefix("Indexing", Red), err)
							}
						} else {
							// stop and restart
							close(reload)
							reload = make(chan bool)
							Record(Prefix("Changed", Magenta), event.Name)
							go a.Reload(a.Tasks, reload)
							ltime = time.Now().Truncate(time.Second)
						}
					}
				}
			}
		case err := <-watcher.Errors():
			a.Options.Recovery.Push(Prefix("Watch Error", Red), err)
		case <-a.Exit:
			// run task after
			a.Reload(a.TasksAfter, reload)
			break L
		}
	}
	return
}

// Exec a command
func (a *Activity) Exec(c Command, w *sync.WaitGroup, reload <-chan bool) (err error) {
	var ex *exec.Cmd
	var lifetime time.Time
	defer func() {
		// https://github.com/golang/go/issues/5615
		// https://github.com/golang/go/issues/6720
		if ex != nil {
			ex.Process.Signal(os.Interrupt)
		}
		// Print command end
		Record(Prefix("Cmd", Green),
			Print("Finished",
			Green.Regular("'")+
			strings.Split(c.Cmd, " -")[0]+
			Green.Regular("'"),
			"in", time.Since(lifetime)))
		// Command done
		w.Done()
	}()
	done := make(chan error)
	// Split command
	args := strings.Split(c.Cmd, " ")
	ex = exec.Command(args[0], args[1:]...)
	// Custom error pattern

	// Get exec dir
	if len(c.Dir) > 0 {
		ex.Dir = c.Dir
	} else {
		dir, err := os.Getwd()
		if err != nil {
			return
		}
		ex.Dir = dir
	}
	// stdout
	stdout, err := ex.StdoutPipe()
	if err != nil {
		return
	}
	// stderr
	stderr, err := ex.StderrPipe()
	if err != nil{
		return
	}
	// Start command
	if err := ex.Start(); err != nil{
		return
	} else{
		// Print command start
		Record(Prefix("Cmd", Green),
			Print("Running",
			Green.Regular("'")+
			strings.Split(c.Cmd, " -")[0]+
			Green.Regular("'")))
		// Start time
		lifetime = time.Now()
	}
	// Scan outputs and errors generated by command exec
	exOut, exErr := bufio.NewScanner(stdout), bufio.NewScanner(stderr)
	stopOut, stopErr := make(chan bool, 1), make(chan bool, 1)
	scanner := func(output *bufio.Scanner, end chan bool, err bool) {
		for output.Scan() {
			if len(output.Text()) > 0 {
				if err {
					// check custom error pattern
					Record(Prefix("Err", Red), errors.New(output.Text()))
				} else {
					Record(Prefix("Out", Blue), output.Text())
				}
			}
		}
		close(end)
	}
	// Wait command end
	go func() { done <- ex.Wait() }()
	// Run scanner
	go scanner(exErr, stopErr,true)
	go scanner(exOut, stopOut,false)

	// Wait command result
	select {
	case <-reload:
		// Stop running command
		ex.Process.Kill()
		break
	case <-done:
		break
	}
	return
}

// Reload exec a list of commands in parallel or in sequence
func (a *Activity) Reload(tasks []interface{}, reload <-chan bool) {
	var w sync.WaitGroup
	w.Add(len(tasks))
	// Loop tasks
	for _, task := range tasks {
		switch t := task.(type) {
		case Parallel:
			for _, c := range t.Commands {
				select {
				case <-reload:
					w.Done()
					break
				default:
					// Exec command
					if len(c.Cmd) > 0 {
						go a.Exec(c, &w, reload)
					}
				}
			}
		case Sequence:
			for _, c := range t.Commands {
				select {
				case <-reload:
					w.Done()
					break
				default:
					// Exec command
					if len(c.Cmd) > 0 {
						a.Exec(c, &w, reload)
					}
				}
			}
		}
	}
	w.Wait()
}

// Validate a path
func (a *Activity) Validate(path string, file bool) (s bool, fi os.FileInfo) {
	if len(path) <= 0 {
		return
	}
	// validate dot
	if a.Ignore.Dot {
		if Dot(path) {
			return
		}
	}
	// validate extension
	if e := Ext(path); e != "" {
		if len(a.Ignore.Exts) > 0 {
			for _, v := range a.Ignore.Exts {
				if v == e {
					return
				}
			}
		}
		if len(a.Watch.Exts) > 0 {
			match := false
			for _, v := range a.Watch.Exts {
				if v == e {
					match = true
					break
				}
			}
			if !match {
				return
			}
		}
	}
	// validate path
	if fpath, err := filepath.Abs(path); err != nil {
		a.Options.Recovery.Push(Prefix("Error", Red), err)
		return
	} else {
		if len(a.Ignore.Paths) > 0 {
			for _, v := range a.Ignore.Paths {
				v, _ := filepath.Abs(v)
				if strings.Contains(fpath, v) {
					return
				}
				if strings.Contains(v, "*") {
					// check glob
					paths, err := filepath.Glob(v)
					if err != nil {
						a.Options.Recovery.Push(Prefix("Error", Red), err)
						return
					}
					for _, p := range paths {
						if strings.Contains(p, fpath) {
							return
						}
					}
				}
			}
		}
		if len(a.Watch.Paths) > 0 {
			match := false
			for _, v := range a.Watch.Paths {
				v, _ := filepath.Abs(v)
				if strings.Contains(fpath, v) {
					match = true
					break
				}
				if strings.Contains(v, "*") {
					// check glob
					paths, err := filepath.Glob(v)
					if err != nil {
						a.Options.Recovery.Push(Prefix("Error", Red), err)
						return
					}
					for _, p := range paths {
						if strings.Contains(p, fpath) {
							match = true
							break
						}
					}
				}
			}
			if !match {
				return
			}
		}
	}
	s = true
	return
}
