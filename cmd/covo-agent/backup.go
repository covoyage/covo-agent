package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/covoyage/covo-agent/internal/cli"
	"github.com/covoyage/covo-agent/internal/tools"
	"github.com/spf13/cobra"
)

func newBackupCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "backup [output-path]",
		Short: "Back up covo-agent data",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := cli.HomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			outputPath := "covo-backup.tar.gz"
			if len(args) > 0 {
				outputPath = args[0]
			}
			if !filepath.IsAbs(outputPath) {
				wd, _ := os.Getwd()
				outputPath = filepath.Join(wd, outputPath)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Creating backup from %s ...\n", homeDir)
			if err := tools.CreateBackup(homeDir, outputPath); err != nil {
				return fmt.Errorf("backup failed: %w", err)
			}
			info, _ := os.Stat(outputPath)
			sizeKB := int64(0)
			if info != nil {
				sizeKB = info.Size() / 1024
			}
			fmt.Fprintf(w, "Backup created: %s (%d KB)\n", outputPath, sizeKB)
			fmt.Fprintln(w)
			fmt.Fprintln(w, "To restore:")
			fmt.Fprintf(w, "  covo-agent restore %s\n", outputPath)
			return nil
		},
	}
}

func newRestoreCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <archive-path> [target-dir]",
		Short: "Restore covo-agent data",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			archivePath := args[0]
			if _, err := os.Stat(archivePath); os.IsNotExist(err) {
				return fmt.Errorf("archive not found: %s", archivePath)
			}
			targetDir := ""
			if len(args) > 1 {
				targetDir = args[1]
			} else {
				var err error
				targetDir, err = cli.HomeDir()
				if err != nil {
					return fmt.Errorf("home dir: %w", err)
				}
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Restoring from %s to %s ...\n", archivePath, targetDir)
			fmt.Fprintln(w, "Existing data will be backed up before restoring.")
			fmt.Fprint(w, "Continue? [y/N] ")
			var confirm string
			fmt.Scanln(&confirm)
			if confirm != "y" && confirm != "Y" && confirm != "yes" {
				fmt.Fprintln(w, "Restore cancelled.")
				return nil
			}
			if err := tools.RestoreBackup(archivePath, targetDir); err != nil {
				return fmt.Errorf("restore failed: %w", err)
			}
			fmt.Fprintln(w, "Restore complete.")
			return nil
		},
	}
}

func newMigrateCommand() *cobra.Command {
	migrateCmd := &cobra.Command{
		Use:   "migrate [archive-path]",
		Short: "Migrate covo-agent data",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := cli.HomeDir()
			if err != nil {
				return fmt.Errorf("home dir: %w", err)
			}
			outputPath := "covo-migrate.tar.gz"
			if len(args) > 0 {
				outputPath = args[0]
			}
			if !filepath.IsAbs(outputPath) {
				wd, _ := os.Getwd()
				outputPath = filepath.Join(wd, outputPath)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Creating migration archive from %s ...\n", homeDir)
			if err := tools.CreateBackup(homeDir, outputPath); err != nil {
				return fmt.Errorf("migration failed: %w", err)
			}
			info, _ := os.Stat(outputPath)
			sizeKB := int64(0)
			if info != nil {
				sizeKB = info.Size() / 1024
			}
			fmt.Fprintf(w, "Migration archive created: %s (%d KB)\n", outputPath, sizeKB)
			fmt.Fprintln(w)
			fmt.Fprintln(w, "To migrate to another machine:")
			fmt.Fprintln(w, "  1. Transfer the archive to the new machine")
			fmt.Fprintln(w, "  2. Install covo-agent on the new machine")
			fmt.Fprintln(w, "  3. Run: covo-agent restore <archive-path>")
			return nil
		},
	}

	var clear bool
	memoryMigrateCmd := &cobra.Command{
		Use:   "memory <source_provider> <dest_provider>",
		Short: "Migrate memory data between providers",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Delegate to the memory command's migrate subcommand
			runArgs := []string{"migrate"}
			if clear {
				runArgs = append(runArgs, "--clear")
			}
			runArgs = append(runArgs, args...)
			cmdMemory(runArgs)
			return nil
		},
	}
	memoryMigrateCmd.Flags().BoolVar(&clear, "clear", false, "clear destination before migrating")
	memoryMigrateCmd.Flags().BoolVar(&clear, "clear-destination", false, "clear destination before migrating")
	migrateCmd.AddCommand(memoryMigrateCmd)

	return migrateCmd
}
