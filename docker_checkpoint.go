package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

// Alternative approach using Docker's experimental checkpoint API
func checkpointContainerDocker(containerID, checkpointDir string) error {
	ctx := context.Background()

	// Create Docker client
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer dockerClient.Close()

	// Get container info
	containerInfo, err := dockerClient.ContainerInspect(ctx, containerID)
	if err != nil {
		return fmt.Errorf("failed to inspect container %s: %w", containerID, err)
	}

	if !containerInfo.State.Running {
		return fmt.Errorf("container %s is not running", containerID)
	}

	// Create checkpoint directory if it doesn't exist
	if err := os.MkdirAll(checkpointDir, 0755); err != nil {
		return fmt.Errorf("failed to create checkpoint directory: %w", err)
	}

	// Save container metadata
	metadataFile := filepath.Join(checkpointDir, "container.info")
	metadata := fmt.Sprintf("CONTAINER_ID=%s\nCONTAINER_NAME=%s\nIMAGE=%s\nPID=%d\n",
		containerID,
		containerInfo.Name,
		containerInfo.Config.Image,
		containerInfo.State.Pid)

	if err := os.WriteFile(metadataFile, []byte(metadata), 0644); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	fmt.Printf("Container %s (PID: %d)\n", containerID, containerInfo.State.Pid)
	fmt.Printf("Attempting Docker native checkpoint...\n")

	// Try using Docker's experimental checkpoint feature
	checkpointName := "manual-checkpoint"
	checkpointCreateOptions := types.CheckpointCreateOptions{
		CheckpointID:  checkpointName,
		CheckpointDir: checkpointDir,
		Exit:          false, // Don't exit the container after checkpoint
	}

	err = dockerClient.CheckpointCreate(ctx, containerID, checkpointCreateOptions)
	if err != nil {
		fmt.Printf("Docker native checkpoint failed: %v\n", err)
		fmt.Printf("Falling back to direct CRIU checkpoint...\n")
		// Fall back to our custom CRIU implementation
		return checkpointContainer(containerID, checkpointDir)
	}

	fmt.Printf("Docker native checkpoint created successfully!\n")

	// List files created
	entries, err := os.ReadDir(checkpointDir)
	if err == nil {
		fmt.Printf("Checkpoint files:\n")
		for _, entry := range entries {
			info, _ := entry.Info()
			fmt.Printf("  - %s (%d bytes)\n", entry.Name(), info.Size())
		}
	}

	return nil
}

func restoreContainerDocker(containerID, checkpointDir string) error {
	ctx := context.Background()

	// Create Docker client
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}
	defer dockerClient.Close()

	fmt.Printf("Attempting Docker native restore...\n")

	// Try using Docker's experimental checkpoint restore
	checkpointName := "manual-checkpoint"
	startOptions := types.ContainerStartOptions{
		CheckpointID:  checkpointName,
		CheckpointDir: checkpointDir,
	}

	err = dockerClient.ContainerStart(ctx, containerID, startOptions)
	if err != nil {
		fmt.Printf("Docker native restore failed: %v\n", err)
		fmt.Printf("Falling back to direct CRIU restore...\n")
		// Fall back to our custom CRIU implementation
		return restoreContainer(containerID, checkpointDir)
	}

	fmt.Printf("Docker native restore completed successfully!\n")

	// Verify container is running
	containerInfo, err := dockerClient.ContainerInspect(ctx, containerID)
	if err != nil {
		return fmt.Errorf("failed to inspect restored container: %w", err)
	}

	if containerInfo.State.Running {
		fmt.Printf("Container %s is running (PID: %d)\n", containerID, containerInfo.State.Pid)
	} else {
		fmt.Printf("Warning: Container restored but not running (State: %s)\n", containerInfo.State.Status)
	}

	return nil
}