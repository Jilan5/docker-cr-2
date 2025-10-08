package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
)

type NotifyHandler struct {
	PreDumpScript    string
	PostDumpScript   string
	PreRestoreScript string
	LogPrefix        string
	Verbose          bool
}

func NewNotifyHandler(verbose bool) *NotifyHandler {
	return &NotifyHandler{
		LogPrefix: "[CRIU Notify]",
		Verbose:   verbose,
	}
}

func (n *NotifyHandler) PreDump() error {
	if n.Verbose {
		log.Printf("%s PreDump called", n.LogPrefix)
	}

	if n.PreDumpScript != "" {
		return n.executeScript(n.PreDumpScript, "PreDump")
	}

	return nil
}

func (n *NotifyHandler) PostDump() error {
	if n.Verbose {
		log.Printf("%s PostDump called", n.LogPrefix)
	}

	if n.PostDumpScript != "" {
		return n.executeScript(n.PostDumpScript, "PostDump")
	}

	return nil
}

func (n *NotifyHandler) PreRestore() error {
	if n.Verbose {
		log.Printf("%s PreRestore called", n.LogPrefix)
	}

	if n.PreRestoreScript != "" {
		return n.executeScript(n.PreRestoreScript, "PreRestore")
	}

	return nil
}

func (n *NotifyHandler) PostRestore(pid int32) error {
	if n.Verbose {
		log.Printf("%s PostRestore called with PID %d", n.LogPrefix, pid)
	}
	return nil
}

func (n *NotifyHandler) NetworkLock() error {
	if n.Verbose {
		log.Printf("%s NetworkLock called", n.LogPrefix)
	}
	return nil
}

func (n *NotifyHandler) NetworkUnlock() error {
	if n.Verbose {
		log.Printf("%s NetworkUnlock called", n.LogPrefix)
	}
	return nil
}

func (n *NotifyHandler) SetupNamespaces(pid int32) error {
	if n.Verbose {
		log.Printf("%s SetupNamespaces called for PID %d", n.LogPrefix, pid)
	}
	return nil
}

func (n *NotifyHandler) PostSetupNamespaces() error {
	if n.Verbose {
		log.Printf("%s PostSetupNamespaces called", n.LogPrefix)
	}
	return nil
}

func (n *NotifyHandler) PostResume() error {
	if n.Verbose {
		log.Printf("%s PostResume called", n.LogPrefix)
	}
	return nil
}

func (n *NotifyHandler) executeScript(script string, phase string) error {
	if _, err := os.Stat(script); os.IsNotExist(err) {
		if n.Verbose {
			log.Printf("%s %s script not found: %s", n.LogPrefix, phase, script)
		}
		return nil
	}

	if n.Verbose {
		log.Printf("%s Executing %s script: %s", n.LogPrefix, phase, script)
	}

	cmd := exec.Command("/bin/sh", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s script failed: %w", phase, err)
	}

	return nil
}