package core

import (
	"testing"
)

func TestActivity_Reload(t *testing.T) {
	// var buf bytes.Buffer
	// log.SetOutput(&buf)
	activity := Activity{}
	reload := make(chan bool)
	tasks := make([]interface{}, 0)
	parallel := Parallel{
		Tasks: intf([]Command{
			{
				Cmd: "go clean",
			},
			{
				Cmd: "go fmt",
			},
		}),
	}
	sequence := Series{
		Tasks: intf([]Command{
			{
				Cmd: "go install",
			},
			{
				Cmd: "go build",
			},
		}),
	}
	tasks = append(tasks, parallel)
	tasks = append(tasks, sequence)
	activity.Reload(reload, tasks...)

}

func TestActivity_Validate(t *testing.T) {
	// Test paths
	paths := map[string]bool{
		"/style.go":          true,
		"./handle.go":        true,
		"/core/options.go":   true,
		"../core/realize.go": true,
		"../core/test.html":  false,
		"notify.go":          false,
		"realize_test.go":    false,
	}
	activity := Activity{
		Ignore: &Ignore{
			Paths: []string{
				"notify.go",
				"*_test.go",
			},
		},
		Watch: &Watch{
			Paths: []string{
				"/style.go",
				"./handle.go",
				"../core/*.go",
				"../**/*.go",
				"../**/*.html",
			},
		},
	}
	for p, s := range paths {
		val, _ := activity.Validate(p, false)
		if val != s {
			t.Fatal("Unexpected result", val, "instead", s, "path", p)
		}
	}
	// Test watch extensions and paths
	activity = Activity{
		Ignore: &Ignore{
			Exts: []string{
				"html",
			},
		},
		Watch: &Watch{
			Exts: []string{
				"go",
			},
			Paths: []string{
				"../test/",
			},
		},
	}
	for p, _ := range paths {
		val, _ := activity.Validate(p, false)
		if val {
			t.Fatal("Unexpected result", val, "path", p)
		}
	}
}
