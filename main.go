package main

import (
	"fmt"
	"os"
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
			fmt.Println("Error: checkpoint requires container ID and checkpoint directory")
			fmt.Println("Usage: docker-cr checkpoint <container-id> <checkpoint-dir>")
			os.Exit(1)
		}
		containerID := os.Args[2]
		checkpointDir := os.Args[3]

		fmt.Printf("Creating checkpoint for container %s in %s...\n", containerID, checkpointDir)
		if err := checkpointContainer(containerID, checkpointDir); err != nil {
			fmt.Printf("Error creating checkpoint: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Checkpoint created successfully!")

	case "restore", "rs":
		if len(os.Args) < 4 {
			fmt.Println("Error: restore requires container ID and checkpoint directory")
			fmt.Println("Usage: docker-cr restore <container-id> <checkpoint-dir>")
			os.Exit(1)
		}
		containerID := os.Args[2]
		checkpointDir := os.Args[3]

		fmt.Printf("Restoring container %s from %s...\n", containerID, checkpointDir)
		if err := restoreContainer(containerID, checkpointDir); err != nil {
			fmt.Printf("Error restoring container: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Container restored successfully!")

	case "help", "-h", "--help":
		printUsage()

	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`Docker Container Checkpoint/Restore Tool

Usage:
  docker-cr <command> [arguments]

Commands:
  checkpoint, cp    Create a checkpoint of a running container
                   Usage: docker-cr checkpoint <container-id> <checkpoint-dir>

  restore, rs      Restore a container from a checkpoint
                   Usage: docker-cr restore <container-id> <checkpoint-dir>

  help, -h         Show this help message

Examples:
  docker-cr checkpoint nginx-container /tmp/checkpoint1
  docker-cr restore nginx-container /tmp/checkpoint1

Note:
  - CRIU must be installed on your system
  - Docker must be running with experimental features enabled
  - Run with sudo if needed for CRIU permissions`)
}