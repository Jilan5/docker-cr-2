package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/checkpoint-restore/go-criu/v7"
	"github.com/checkpoint-restore/go-criu/v7/rpc"
	"github.com/docker/docker/client"
	"google.golang.org/protobuf/proto"
)

func checkpointContainer(containerID, checkpointDir string) error {
	// Use the Docker-specific checkpointing function
	return checkpointDockerContainer(containerID, checkpointDir)
}

func checkpointProcess(pid int, checkpointDir string) error {
	criuClient := criu.MakeCriu()

	_, err := criuClient.GetCriuVersion()
	if err != nil {
		return fmt.Errorf("failed to get CRIU version (is CRIU installed?): %w", err)
	}
	fmt.Printf("CRIU version check passed\n")

	if err := criuClient.Prepare(); err != nil {
		return fmt.Errorf("failed to prepare CRIU: %w", err)
	}
	defer criuClient.Cleanup()

	imageDir, err := os.Open(checkpointDir)
	if err != nil {
		return fmt.Errorf("failed to open checkpoint directory: %w", err)
	}
	defer imageDir.Close()

	opts := &rpc.CriuOpts{
		Pid:          proto.Int32(int32(pid)),
		ImagesDirFd:  proto.Int32(int32(imageDir.Fd())),
		LogLevel:     proto.Int32(4),
		LogFile:      proto.String("dump.log"),
		LeaveRunning: proto.Bool(true),
		GhostLimit:   proto.Uint32(10000000),
	}

	if err := prepareProcessForDump(pid, opts); err != nil {
		return fmt.Errorf("failed to prepare process for dump: %w", err)
	}

	notify := NewNotifyHandler(true)

	fmt.Println("Creating checkpoint...")
	err = criuClient.Dump(opts, notify)
	if err != nil {
		logPath := filepath.Join(checkpointDir, "dump.log")
		if logData, readErr := os.ReadFile(logPath); readErr == nil {
			fmt.Printf("CRIU log output:\n%s\n", string(logData))
		}
		return fmt.Errorf("checkpoint failed: %w", err)
	}

	entries, err := os.ReadDir(checkpointDir)
	if err != nil {
		return fmt.Errorf("failed to read checkpoint directory: %w", err)
	}

	fmt.Printf("Checkpoint created with %d files\n", len(entries))
	fmt.Println("Checkpoint files:")
	for _, entry := range entries {
		info, _ := entry.Info()
		fmt.Printf("  - %s (%d bytes)\n", entry.Name(), info.Size())
	}

	return nil
}

func checkpointSimpleProcess(pid int, checkpointDir string) error {
	if err := os.MkdirAll(checkpointDir, 0755); err != nil {
		return fmt.Errorf("failed to create checkpoint directory: %w", err)
	}

	criuClient := criu.MakeCriu()

	_, err := criuClient.GetCriuVersion()
	if err != nil {
		return fmt.Errorf("failed to get CRIU version: %w", err)
	}
	fmt.Printf("CRIU version check passed\n")

	if err := criuClient.Prepare(); err != nil {
		return fmt.Errorf("failed to prepare CRIU: %w", err)
	}
	defer criuClient.Cleanup()

	imageDir, err := os.Open(checkpointDir)
	if err != nil {
		return fmt.Errorf("failed to open checkpoint directory: %w", err)
	}
	defer imageDir.Close()

	opts := &rpc.CriuOpts{
		Pid:         proto.Int32(int32(pid)),
		ImagesDirFd: proto.Int32(int32(imageDir.Fd())),
		LogLevel:    proto.Int32(4),
		LogFile:     proto.String("dump.log"),
	}

	if err := prepareProcessForDump(pid, opts); err != nil {
		return fmt.Errorf("failed to prepare process: %w", err)
	}

	notify := NewNotifyHandler(true)

	fmt.Println("Creating checkpoint...")
	err = criuClient.Dump(opts, notify)
	if err != nil {
		logPath := filepath.Join(checkpointDir, "dump.log")
		if logData, readErr := os.ReadFile(logPath); readErr == nil {
			fmt.Printf("CRIU log:\n%s\n", string(logData))
		}
		return fmt.Errorf("checkpoint failed: %w", err)
	}

	fmt.Println("Checkpoint created successfully!")
	return nil
}