package cmd

import (
	"context"

	"github.com/navidrome/navidrome/log"
	"github.com/spf13/cobra"
)

var (
	backupDir   string
	backupCount int
	force       bool
	restorePath string
)

func init() {
	rootCmd.AddCommand(backupRoot)
	backupCmd.Flags().StringVarP(&backupDir, "backup-dir", "d", "", "directory to manually make backup")
	backupRoot.AddCommand(backupCmd)
	pruneCmd.Flags().StringVarP(&backupDir, "backup-dir", "d", "", "directory holding backups")
	pruneCmd.Flags().IntVarP(&backupCount, "keep-count", "k", -1, "number of backups to keep")
	pruneCmd.Flags().BoolVarP(&force, "force", "f", false, "bypass warning")
	backupRoot.AddCommand(pruneCmd)
	restoreCommand.Flags().StringVarP(&restorePath, "backup-file", "b", "", "path of backup to restore")
	restoreCommand.Flags().BoolVarP(&force, "force", "f", false, "bypass restore warning")
	backupRoot.AddCommand(restoreCommand)
}

var (
	backupRoot     = &cobra.Command{Use: "backup", Aliases: []string{"bkp"}, Short: "MongoDB backup helpers", Long: "MongoDB backup helpers"}
	backupCmd      = &cobra.Command{Use: "create", Short: "Explain MongoDB backup", Run: func(cmd *cobra.Command, _ []string) { runBackup(cmd.Context()) }}
	pruneCmd       = &cobra.Command{Use: "prune", Short: "Explain MongoDB backup pruning", Run: func(cmd *cobra.Command, _ []string) { runPrune(cmd.Context()) }}
	restoreCommand = &cobra.Command{Use: "restore", Short: "Explain MongoDB restore", Run: func(cmd *cobra.Command, _ []string) { runRestore(cmd.Context()) }}
)

func runBackup(ctx context.Context) {
	log.Warn(ctx, "MongoDB is the only database. Use mongodump against MONGODB_URI/MONGODB_DATABASE for backups.", "backupDir", backupDir)
}

func runPrune(ctx context.Context) {
	log.Warn(ctx, "MongoDB backup pruning is external to Navidrome. Prune mongodump artifacts in your backup storage.", "backupDir", backupDir, "keepCount", backupCount, "force", force)
}

func runRestore(ctx context.Context) {
	log.Warn(ctx, "MongoDB restore is external to Navidrome. Use mongorestore against MONGODB_URI/MONGODB_DATABASE.", "backupFile", restorePath, "force", force)
}
