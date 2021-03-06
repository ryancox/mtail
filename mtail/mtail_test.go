// Copyright 2011 Google Inc. All Rights Reserved.
// This file is available under the Apache license.

package mtail

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/mtail/vm"
)

const testProgram = "/$/ { }\n"

func makeTempDir(t *testing.T) (workdir string) {
	var err error
	if workdir, err = ioutil.TempDir("", "mtail_test"); err != nil {
		t.Fatalf("ioutil.TempDir failed: %s", err)
	}
	return
}

func removeTempDir(t *testing.T, workdir string) {
	if err := os.RemoveAll(workdir); err != nil {
		t.Fatalf("os.RemoveAll failed: %s", err)
	}
}

func startMtail(t *testing.T, logPathnames []string, progPathname string) *Mtail {
	o := Options{LogPaths: logPathnames}
	m, err := New(o)
	if err != nil {
		t.Fatalf("couldn't create mtail: %s", err)
	}

	if progPathname != "" {
		m.l.LoadProgs(progPathname)
	} else {
		if pErr := m.l.CompileAndRun("test", strings.NewReader(testProgram)); pErr != nil {
			t.Errorf("Couldn't compile program: %s", pErr)
		}
	}

	vm.LineCount.Set(0)

	m.StartTailing()
	return m
}

func doOrTimeout(do func() (bool, error), deadline, interval time.Duration) (bool, error) {
	timeout := time.After(deadline)
	ticker := time.Tick(interval)
	for {
		select {
		case <-timeout:
			return false, errors.New("timeout")
		case <-ticker:
			ok, err := do()
			if err != nil {
				return false, err
			} else if ok {
				return true, nil
			}
		}
	}
}

func TestHandleLogUpdates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}
	workdir := makeTempDir(t)
	defer removeTempDir(t, workdir)
	// touch log file
	logFilepath := path.Join(workdir, "log")
	logFile, err := os.Create(logFilepath)
	if err != nil {
		t.Errorf("could not touch log file: %s", err)
	}
	defer logFile.Close()
	pathnames := []string{logFilepath}
	m := startMtail(t, pathnames, "")
	defer m.Close()
	inputLines := []string{"hi", "hi2", "hi3"}
	for i, x := range inputLines {
		// write to log file
		logFile.WriteString(x + "\n")
		// check log line count increase
		expected := fmt.Sprintf("%d", i+1)
		check := func() (bool, error) {
			if vm.LineCount.String() != expected {
				return false, nil
			}
			return true, nil
		}
		ok, err := doOrTimeout(check, 100*time.Millisecond, 10*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Errorf("Line count not increased\n\texpected: %s\n\treceived: %s", expected, vm.LineCount.String())
			buf := make([]byte, 1<<16)
			count := runtime.Stack(buf, true)
			fmt.Println(string(buf[:count]))
		}
	}
}

func TestHandleLogRotation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}
	workdir := makeTempDir(t)
	defer removeTempDir(t, workdir)
	logFilepath := path.Join(workdir, "log")
	// touch log file
	logFile, err := os.Create(logFilepath)
	if err != nil {
		t.Errorf("could not touch log file: %s", err)
	}
	defer logFile.Close()
	// Create a logger
	stop := make(chan bool, 1)
	hup := make(chan bool, 1)
	pathnames := []string{logFilepath}
	m := startMtail(t, pathnames, "")
	defer m.Close()

	go func() {
		logFile := logFile
		var err error
		i := 0
		running := true
		for running {
			select {
			case <-hup:
				// touch log file
				logFile, err = os.Create(logFilepath)
				if err != nil {
					t.Errorf("could not touch log file: %s", err)
				}
				defer logFile.Close()
			default:
				logFile.WriteString(fmt.Sprintf("%d\n", i))
				time.Sleep(100 * time.Millisecond)
				i++
				if i >= 10 {
					running = false
				}
			}
		}
		stop <- true
	}()
	go func() {
		for {
			select {
			case <-time.After(5 * 100 * time.Millisecond):
				err = os.Rename(logFilepath, logFilepath+".1")
				if err != nil {
					t.Errorf("could not rename log file: %s", err)
				}
				hup <- true
				return
			}
		}
	}()
	<-stop
	expected := "10"
	if vm.LineCount.String() != expected {
		t.Errorf("Line count not increased\n\texpected: %s\n\treceived: %s", expected, vm.LineCount.String())
	}
}

func TestHandleNewLogAfterStart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	workdir := makeTempDir(t)
	defer removeTempDir(t, workdir)
	// Start up mtail
	logFilepath := path.Join(workdir, "log")
	pathnames := []string{logFilepath}
	m := startMtail(t, pathnames, "")
	defer m.Close()

	// touch log file
	logFile, err := os.Create(logFilepath)
	if err != nil {
		t.Errorf("could not touch log file: %s", err)
	}
	defer logFile.Close()
	inputLines := []string{"hi", "hi2", "hi3"}
	for _, x := range inputLines {
		// write to log file
		logFile.WriteString(x + "\n")
		logFile.Sync()
	}
	// check log line count increase
	expected := fmt.Sprintf("%d", len(inputLines))
	check := func() (bool, error) {
		if vm.LineCount.String() != expected {
			return false, nil
		}
		return true, nil
	}
	ok, err := doOrTimeout(check, 100*time.Millisecond, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Errorf("Line count not increased\n\texpected: %s\n\treceived: %s", expected, vm.LineCount.String())
	}
}

func TestHandleNewLogIgnored(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}
	workdir := makeTempDir(t)
	defer removeTempDir(t, workdir)
	// Start mtail
	logFilepath := path.Join(workdir, "log")
	pathnames := []string{logFilepath}
	m := startMtail(t, pathnames, "")
	defer m.Close()

	// touch log file
	newLogFilepath := path.Join(workdir, "log1")

	logFile, err := os.Create(newLogFilepath)
	if err != nil {
		t.Errorf("could not touch log file: %s", err)
	}
	defer logFile.Close()
	expected := "0"
	if vm.LineCount.String() != expected {
		t.Errorf("Line count not increased\n\texpected: %s\n\treceived: %s", expected, vm.LineCount.String())
	}
}
