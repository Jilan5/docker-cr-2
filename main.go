package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "checkpoint", "cp":
		if len(os.Args) < 4 {
			fmt.Println("Error: checkpoint requires container ID/PID and checkpoint directory")
			fmt.Println("Usage: docker-cr checkpoint <container-id|pid> <checkpoint-dir>")
			os.Exit(1)
		}
		target := os.Args[2]
		checkpointDir := os.Args[3]

		if pid, err := strconv.Atoi(target); err == nil {
			fmt.Printf("Creating checkpoint for process %d in %s...\n", pid, checkpointDir)
			if err := checkpointSimpleProcess(pid, checkpointDir); err != nil {
				fmt.Printf("Error creating checkpoint: %v\n", err)
				os.Exit(1)
			}
		} else {
			fmt.Printf("Creating checkpoint for container %s in %s...\n", target, checkpointDir)
			if err := checkpointContainer(target, checkpointDir); err != nil {
				fmt.Printf("Error creating checkpoint: %v\n", err)
				os.Exit(1)
			}
		}
		fmt.Println("Checkpoint created successfully!")

	case "restore", "rs":
		if len(os.Args) < 3 {
			fmt.Println("Error: restore requires checkpoint directory")
			fmt.Println("Usage: docker-cr restore <checkpoint-dir> [container-id]")
			os.Exit(1)
		}
		checkpointDir := os.Args[2]

		if len(os.Args) >= 4 {
			containerID := os.Args[3]
			fmt.Printf("Restoring container %s from %s...\n", containerID, checkpointDir)
			if err := restoreContainer(containerID, checkpointDir); err != nil {
				fmt.Printf("Error restoring container: %v\n", err)
				os.Exit(1)
			}
		} else {
			fmt.Printf("Restoring process from %s...\n", checkpointDir)
			if err := restoreSimpleProcess(checkpointDir); err != nil {
				fmt.Printf("Error restoring process: %v\n", err)
				os.Exit(1)
			}
		}
		fmt.Println("Restore completed successfully!")

	case "help", "-h", "--help":
		printUsage()

	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`Docker Container & Process Checkpoint/Restore Tool

Usage:
  docker-cr <command> [arguments]

Commands:
  checkpoint, cp    Create a checkpoint of a running container or process
                   Usage: docker-cr checkpoint <container-id|pid> <checkpoint-dir>

                   Examples:
                     docker-cr checkpoint nginx-container /tmp/checkpoint1
                     docker-cr checkpoint 12345 /tmp/checkpoint1

  restore, rs      Restore a container or process from a checkpoint
                   Usage: docker-cr restore <checkpoint-dir> [container-id]

                   Examples:
                     docker-cr restore /tmp/checkpoint1
                     docker-cr restore /tmp/checkpoint1 nginx-container

  help, -h         Show this help message

Requirements:
  - CRIU must be installed on your system (apt install criu)
  - Docker must be running with experimental features enabled
  - Run with sudo for CRIU permissions

Docker Setup:
  Enable experimental features in Docker:
  echo '{"experimental": true}' | sudo tee /etc/docker/daemon.json
  sudo systemctl restart docker

Testing with a Simple Process:
  1. Start a simple process: sleep 1000 &
  2. Note the PID (e.g., 12345)
  3. Checkpoint: sudo docker-cr checkpoint 12345 /tmp/test
  4. Kill the process: kill 12345
  5. Restore: sudo docker-cr restore /tmp/test

Notes:
  - The tool automatically detects TCP connections and Unix sockets
  - Processes are kept running during checkpoint by default
  - Comprehensive logging is provided for debugging`)
}