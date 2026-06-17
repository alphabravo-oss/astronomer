// Backup subcommands: backups, restores, schedules, storage configs, and
// the backup controller status. Every leaf command builds the generated
// SDK client via the shared newAstroClient helper, calls the typed method,
// and renders the result honoring the global -o/--output flag.
//
// Mirrors cluster.go's structure (a parent cobra.Command per noun with
// list/get/create/update/delete leaves) but talks to the typed
// pkg/astroclient SDK instead of the legacy astrocli.Client.

package main

import (
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
)

// newBackupCmd is the backup command group root. A later integration step
// wires this into the root command (main.go is intentionally untouched).
func newBackupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "backup",
		Aliases: []string{"backups"},
		Short:   "Manage backups, schedules, and storage configuration",
	}
	cmd.AddCommand(
		newBackupListCmd(),
		newBackupGetCmd(),
		newBackupCreateCmd(),
		newBackupDeleteCmd(),
		newBackupRestoreCmd(),
		newBackupRunsCmd(),
		newBackupRestoresCmd(),
		newBackupSchedulesCmd(),
		newBackupStorageCmd(),
		newBackupStorageConfigsCmd(),
		newBackupControllerStatusCmd(),
	)
	return cmd
}

// ---------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------

// bkParseUUID converts a CLI argument into the SDK's UUID type, returning a
// clean error (mapped to a non-zero exit by cobra) on a malformed id.
func bkParseUUID(arg string) (uuid.UUID, error) {
	id, err := uuid.Parse(arg)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("invalid id %q: must be a UUID", arg)
	}
	return id, nil
}

// bkSDKError turns an unexpected (non-2xx, non-typed) SDK response into a
// readable error. It prefers the decoded ErrorEnvelope message, then falls
// back to the raw body, then the bare HTTP status.
func bkSDKError(status *http.Response, env *astroclient.ErrorResponse, body []byte) error {
	if env != nil && env.Error.Message != "" {
		if env.Error.Code != "" {
			return fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
		}
		return fmt.Errorf("%s", env.Error.Message)
	}
	if len(body) > 0 {
		return fmt.Errorf("request failed: %s", string(body))
	}
	if status != nil {
		return fmt.Errorf("request failed: %s", status.Status)
	}
	return fmt.Errorf("request failed")
}

// bkLimitOffsetFlags registers the standard pagination flags and returns the
// bound int pointers ready to drop into an SDK params struct.
func bkLimitOffsetFlags(cmd *cobra.Command) (limit, offset *int) {
	limit = cmd.Flags().Int("limit", 0, "max items to return")
	offset = cmd.Flags().Int("offset", 0, "items to skip")
	return limit, offset
}

// bkPageParam returns a pointer to the flag value only when the flag was set,
// so an unset --limit/--offset is omitted from the query string entirely.
func bkPageParam(cmd *cobra.Command, name string, v *int) *int {
	if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
		return v
	}
	return nil
}

// ---------------------------------------------------------------------
// backups: list / get / create / delete / restore / runs / restores
// ---------------------------------------------------------------------

func newBackupListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List backups",
		Args:  cobra.NoArgs,
	}
	limit, offset := bkLimitOffsetFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		client, err := newAstroClient(cmd)
		if err != nil {
			return err
		}
		params := &astroclient.GetApiV1BackupsParams{
			Limit:  bkPageParam(cmd, "limit", limit),
			Offset: bkPageParam(cmd, "offset", offset),
		}
		resp, err := client.GetApiV1BackupsWithResponse(cmd.Context(), params)
		if err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return bkSDKError(resp.HTTPResponse, resp.JSON500, resp.Body)
		}
		return renderSDK(cmd, resp.JSON200.Data)
	}
	return cmd
}

func newBackupGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one backup's details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := bkParseUUID(args[0])
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1BackupsIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return bkSDKError(resp.HTTPResponse, bkFirstErr(resp.JSON400, resp.JSON404), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

func newBackupCreateCmd() *cobra.Command {
	var (
		name               string
		storageID          string
		backupType         string
		includedNamespaces []string
		excludedNamespaces []string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a backup",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			sid, err := bkParseUUID(storageID)
			if err != nil {
				return fmt.Errorf("--storage-id: %w", err)
			}
			body := astroclient.BackupCreateRequest{
				Name:      name,
				StorageId: sid,
			}
			if backupType != "" {
				body.BackupType = &backupType
			}
			if len(includedNamespaces) > 0 {
				body.IncludedNamespaces = &includedNamespaces
			}
			if len(excludedNamespaces) > 0 {
				body.ExcludedNamespaces = &excludedNamespaces
			}
			resp, err := client.PostApiV1BackupsWithResponse(cmd.Context(), body)
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return bkSDKError(resp.HTTPResponse, bkFirstErr(resp.JSON400, resp.JSON404, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON201.Data)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "backup name (required)")
	cmd.Flags().StringVar(&storageID, "storage-id", "", "storage config UUID (required)")
	cmd.Flags().StringVar(&backupType, "backup-type", "", "backup type")
	cmd.Flags().StringSliceVar(&includedNamespaces, "included-namespaces", nil, "namespaces to include")
	cmd.Flags().StringSliceVar(&excludedNamespaces, "excluded-namespaces", nil, "namespaces to exclude")
	return cmd
}

func newBackupDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a backup",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := bkParseUUID(args[0])
			if err != nil {
				return err
			}
			resp, err := client.DeleteApiV1BackupsIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if !bkOKStatus(resp.HTTPResponse) {
				return bkSDKError(resp.HTTPResponse, bkFirstErr(resp.JSON400, resp.JSON500), resp.Body)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted backup %s.\n", args[0])
			return err
		},
	}
	return cmd
}

func newBackupRestoreCmd() *cobra.Command {
	var (
		includedNamespaces []string
		namespaceMapping   map[string]string
	)
	cmd := &cobra.Command{
		Use:   "restore <id>",
		Short: "Restore from a backup",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := bkParseUUID(args[0])
			if err != nil {
				return err
			}
			body := astroclient.BackupRestoreRequest{}
			if len(includedNamespaces) > 0 {
				body.IncludedNamespaces = &includedNamespaces
			}
			if len(namespaceMapping) > 0 {
				body.NamespaceMapping = &namespaceMapping
			}
			resp, err := client.PostApiV1BackupsIdRestoreWithResponse(cmd.Context(), id, body)
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return bkSDKError(resp.HTTPResponse, bkFirstErr(resp.JSON400, resp.JSON404, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON201.Data)
		},
	}
	cmd.Flags().StringSliceVar(&includedNamespaces, "included-namespaces", nil, "namespaces to restore")
	cmd.Flags().StringToStringVar(&namespaceMapping, "namespace-mapping", nil, "old=new namespace remap")
	return cmd
}

func newBackupRunsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "List backup runs",
		Args:  cobra.NoArgs,
	}
	limit, offset := bkLimitOffsetFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		client, err := newAstroClient(cmd)
		if err != nil {
			return err
		}
		params := &astroclient.GetApiV1BackupsRunsParams{
			Limit:  bkPageParam(cmd, "limit", limit),
			Offset: bkPageParam(cmd, "offset", offset),
		}
		resp, err := client.GetApiV1BackupsRunsWithResponse(cmd.Context(), params)
		if err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return bkSDKError(resp.HTTPResponse, resp.JSON500, resp.Body)
		}
		return renderSDK(cmd, resp.JSON200.Data)
	}
	return cmd
}

func newBackupRestoresCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restores",
		Short: "List restore operations",
		Args:  cobra.NoArgs,
	}
	limit, offset := bkLimitOffsetFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		client, err := newAstroClient(cmd)
		if err != nil {
			return err
		}
		params := &astroclient.GetApiV1BackupsRestoresParams{
			Limit:  bkPageParam(cmd, "limit", limit),
			Offset: bkPageParam(cmd, "offset", offset),
		}
		resp, err := client.GetApiV1BackupsRestoresWithResponse(cmd.Context(), params)
		if err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return bkSDKError(resp.HTTPResponse, resp.JSON500, resp.Body)
		}
		return renderSDK(cmd, resp.JSON200.Data)
	}
	return cmd
}

// ---------------------------------------------------------------------
// schedules: list / get / create / update / delete / trigger
// ---------------------------------------------------------------------

func newBackupSchedulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "schedules",
		Aliases: []string{"schedule"},
		Short:   "Manage backup schedules",
	}
	cmd.AddCommand(
		newScheduleListCmd(),
		newScheduleGetCmd(),
		newScheduleCreateCmd(),
		newScheduleUpdateCmd(),
		newScheduleDeleteCmd(),
		newScheduleTriggerCmd(),
	)
	return cmd
}

func newScheduleListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List backup schedules",
		Args:  cobra.NoArgs,
	}
	limit, offset := bkLimitOffsetFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		client, err := newAstroClient(cmd)
		if err != nil {
			return err
		}
		params := &astroclient.GetApiV1BackupsSchedulesParams{
			Limit:  bkPageParam(cmd, "limit", limit),
			Offset: bkPageParam(cmd, "offset", offset),
		}
		resp, err := client.GetApiV1BackupsSchedulesWithResponse(cmd.Context(), params)
		if err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return bkSDKError(resp.HTTPResponse, resp.JSON500, resp.Body)
		}
		return renderSDK(cmd, resp.JSON200.Data)
	}
	return cmd
}

func newScheduleGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one backup schedule",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := bkParseUUID(args[0])
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1BackupsSchedulesIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return bkSDKError(resp.HTTPResponse, bkFirstErr(resp.JSON400, resp.JSON404), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

// scheduleFlags binds the shared create/update schedule flags onto a
// cobra.Command and returns a closure that assembles the request body. The
// closure validates the fields the server marks required (name, cron,
// storage-id) for create; update reuses the same full-replacement body.
func scheduleFlags(cmd *cobra.Command) func() (astroclient.BackupScheduleRequest, error) {
	var (
		name           string
		cron           string
		storageID      string
		backupType     string
		clusterID      string
		enabled        bool
		retentionCount int32
		ttl            string
		included       []string
		excluded       []string
	)
	cmd.Flags().StringVar(&name, "name", "", "schedule name (required)")
	cmd.Flags().StringVar(&cron, "cron", "", "cron expression (required)")
	cmd.Flags().StringVar(&storageID, "storage-id", "", "storage config UUID (required)")
	cmd.Flags().StringVar(&backupType, "backup-type", "", "backup type")
	cmd.Flags().StringVar(&clusterID, "cluster-id", "", "target cluster UUID")
	cmd.Flags().BoolVar(&enabled, "enabled", true, "whether the schedule is enabled")
	cmd.Flags().Int32Var(&retentionCount, "retention-count", 0, "backups to retain")
	cmd.Flags().StringVar(&ttl, "ttl", "", "time-to-live for produced backups")
	cmd.Flags().StringSliceVar(&included, "included-namespaces", nil, "namespaces to include")
	cmd.Flags().StringSliceVar(&excluded, "excluded-namespaces", nil, "namespaces to exclude")

	return func() (astroclient.BackupScheduleRequest, error) {
		var body astroclient.BackupScheduleRequest
		if name == "" {
			return body, fmt.Errorf("--name is required")
		}
		if cron == "" {
			return body, fmt.Errorf("--cron is required")
		}
		sid, err := bkParseUUID(storageID)
		if err != nil {
			return body, fmt.Errorf("--storage-id: %w", err)
		}
		body.Name = name
		body.CronExpression = cron
		body.StorageId = sid
		if backupType != "" {
			body.BackupType = &backupType
		}
		if clusterID != "" {
			cid, err := bkParseUUID(clusterID)
			if err != nil {
				return body, fmt.Errorf("--cluster-id: %w", err)
			}
			body.ClusterId = &cid
		}
		if f := cmd.Flags().Lookup("enabled"); f != nil && f.Changed {
			body.Enabled = &enabled
		}
		if f := cmd.Flags().Lookup("retention-count"); f != nil && f.Changed {
			body.RetentionCount = &retentionCount
		}
		if ttl != "" {
			body.Ttl = &ttl
		}
		if len(included) > 0 {
			body.IncludedNamespaces = &included
		}
		if len(excluded) > 0 {
			body.ExcludedNamespaces = &excluded
		}
		return body, nil
	}
}

func newScheduleCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a backup schedule",
		Args:  cobra.NoArgs,
	}
	build := scheduleFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		client, err := newAstroClient(cmd)
		if err != nil {
			return err
		}
		body, err := build()
		if err != nil {
			return err
		}
		resp, err := client.PostApiV1BackupsSchedulesWithResponse(cmd.Context(), body)
		if err != nil {
			return err
		}
		if resp.JSON201 == nil {
			return bkSDKError(resp.HTTPResponse, bkFirstErr(resp.JSON400, resp.JSON404, resp.JSON500), resp.Body)
		}
		return renderSDK(cmd, resp.JSON201.Data)
	}
	return cmd
}

func newScheduleUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a backup schedule (full replacement)",
		Args:  cobra.ExactArgs(1),
	}
	build := scheduleFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		client, err := newAstroClient(cmd)
		if err != nil {
			return err
		}
		id, err := bkParseUUID(args[0])
		if err != nil {
			return err
		}
		body, err := build()
		if err != nil {
			return err
		}
		resp, err := client.PutApiV1BackupsSchedulesIdWithResponse(cmd.Context(), id, body)
		if err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return bkSDKError(resp.HTTPResponse, bkFirstErr(resp.JSON400, resp.JSON404, resp.JSON500), resp.Body)
		}
		return renderSDK(cmd, resp.JSON200.Data)
	}
	return cmd
}

func newScheduleDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a backup schedule",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := bkParseUUID(args[0])
			if err != nil {
				return err
			}
			resp, err := client.DeleteApiV1BackupsSchedulesIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if !bkOKStatus(resp.HTTPResponse) {
				return bkSDKError(resp.HTTPResponse, bkFirstErr(resp.JSON400, resp.JSON500), resp.Body)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted schedule %s.\n", args[0])
			return err
		},
	}
	return cmd
}

func newScheduleTriggerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "trigger <id>",
		Aliases: []string{"trigger-now"},
		Short:   "Trigger a backup schedule immediately",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := bkParseUUID(args[0])
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1BackupsSchedulesIdTriggerNowWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return bkSDKError(resp.HTTPResponse, bkFirstErr(resp.JSON400, resp.JSON404, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON201.Data)
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// storage: list / get / create / update / delete / test
// ---------------------------------------------------------------------

func newBackupStorageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "storage",
		Short: "Manage backup storage (the /storage endpoints)",
	}
	cmd.AddCommand(
		newStorageListCmd(),
		newStorageGetCmd(),
		newStorageCreateCmd(),
		newStorageUpdateCmd(),
		newStorageDeleteCmd(),
		newStorageTestCmd(),
	)
	return cmd
}

func newStorageListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List storage configurations",
		Args:  cobra.NoArgs,
	}
	limit, offset := bkLimitOffsetFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		client, err := newAstroClient(cmd)
		if err != nil {
			return err
		}
		params := &astroclient.GetApiV1BackupsStorageParams{
			Limit:  bkPageParam(cmd, "limit", limit),
			Offset: bkPageParam(cmd, "offset", offset),
		}
		resp, err := client.GetApiV1BackupsStorageWithResponse(cmd.Context(), params)
		if err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return bkSDKError(resp.HTTPResponse, resp.JSON500, resp.Body)
		}
		return renderSDK(cmd, resp.JSON200.Data)
	}
	return cmd
}

func newStorageGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one storage configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := bkParseUUID(args[0])
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1BackupsStorageIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return bkSDKError(resp.HTTPResponse, bkFirstErr(resp.JSON400, resp.JSON404), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

// storageConfigFlags binds the shared create/update storage flags and
// returns a builder. bucket and name are server-required.
func storageConfigFlags(cmd *cobra.Command) func() (astroclient.BackupStorageConfigRequest, error) {
	var (
		name        string
		bucket      string
		storageType string
		region      string
		endpointURL string
		prefix      string
		accessKey   string
		secretKey   string
		bslName     string
		clusterID   string
		isDefault   bool
	)
	cmd.Flags().StringVar(&name, "name", "", "config name (required)")
	cmd.Flags().StringVar(&bucket, "bucket", "", "bucket name (required)")
	cmd.Flags().StringVar(&storageType, "storage-type", "", "storage backend (s3, gcs, ...)")
	cmd.Flags().StringVar(&region, "region", "", "bucket region")
	cmd.Flags().StringVar(&endpointURL, "endpoint-url", "", "custom S3-compatible endpoint")
	cmd.Flags().StringVar(&prefix, "prefix", "", "object key prefix")
	cmd.Flags().StringVar(&accessKey, "access-key", "", "access key id")
	cmd.Flags().StringVar(&secretKey, "secret-key", "", "secret access key")
	cmd.Flags().StringVar(&bslName, "bsl-name", "", "velero backup storage location name")
	cmd.Flags().StringVar(&clusterID, "cluster-id", "", "target cluster UUID")
	cmd.Flags().BoolVar(&isDefault, "default", false, "mark as the default storage config")

	return func() (astroclient.BackupStorageConfigRequest, error) {
		var body astroclient.BackupStorageConfigRequest
		if name == "" {
			return body, fmt.Errorf("--name is required")
		}
		if bucket == "" {
			return body, fmt.Errorf("--bucket is required")
		}
		body.Name = name
		body.Bucket = bucket
		if storageType != "" {
			body.StorageType = &storageType
		}
		if region != "" {
			body.Region = &region
		}
		if endpointURL != "" {
			body.EndpointUrl = &endpointURL
		}
		if prefix != "" {
			body.Prefix = &prefix
		}
		if accessKey != "" {
			body.AccessKey = &accessKey
		}
		if secretKey != "" {
			body.SecretKey = &secretKey
		}
		if bslName != "" {
			body.BslName = &bslName
		}
		if clusterID != "" {
			cid, err := bkParseUUID(clusterID)
			if err != nil {
				return body, fmt.Errorf("--cluster-id: %w", err)
			}
			body.ClusterId = &cid
		}
		if f := cmd.Flags().Lookup("default"); f != nil && f.Changed {
			body.IsDefault = &isDefault
		}
		return body, nil
	}
}

func newStorageCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a storage configuration",
		Args:  cobra.NoArgs,
	}
	build := storageConfigFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		client, err := newAstroClient(cmd)
		if err != nil {
			return err
		}
		body, err := build()
		if err != nil {
			return err
		}
		resp, err := client.PostApiV1BackupsStorageWithResponse(cmd.Context(), body)
		if err != nil {
			return err
		}
		if resp.JSON201 == nil {
			return bkSDKError(resp.HTTPResponse, bkFirstErr(resp.JSON400, resp.JSON500), resp.Body)
		}
		return renderSDK(cmd, resp.JSON201.Data)
	}
	return cmd
}

func newStorageUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a storage configuration (full replacement)",
		Args:  cobra.ExactArgs(1),
	}
	build := storageConfigFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		client, err := newAstroClient(cmd)
		if err != nil {
			return err
		}
		id, err := bkParseUUID(args[0])
		if err != nil {
			return err
		}
		body, err := build()
		if err != nil {
			return err
		}
		resp, err := client.PutApiV1BackupsStorageIdWithResponse(cmd.Context(), id, body)
		if err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return bkSDKError(resp.HTTPResponse, bkFirstErr(resp.JSON400, resp.JSON500), resp.Body)
		}
		return renderSDK(cmd, resp.JSON200.Data)
	}
	return cmd
}

func newStorageDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a storage configuration",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := bkParseUUID(args[0])
			if err != nil {
				return err
			}
			resp, err := client.DeleteApiV1BackupsStorageIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if !bkOKStatus(resp.HTTPResponse) {
				return bkSDKError(resp.HTTPResponse, bkFirstErr(resp.JSON400, resp.JSON500), resp.Body)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted storage config %s.\n", args[0])
			return err
		},
	}
	return cmd
}

// newStorageTestCmd hits the /storage/{id}/test endpoint. The
// /storage/{id}/test-connection endpoint also exists; it is exposed below
// as a hidden alias so both generated methods are reachable from the CLI.
func newStorageTestCmd() *cobra.Command {
	var useTestConnection bool
	cmd := &cobra.Command{
		Use:   "test <id>",
		Short: "Test a storage configuration's connectivity",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := bkParseUUID(args[0])
			if err != nil {
				return err
			}
			if useTestConnection {
				resp, err := client.PostApiV1BackupsStorageIdTestConnectionWithResponse(cmd.Context(), id)
				if err != nil {
					return err
				}
				if resp.JSON200 == nil {
					return bkSDKError(resp.HTTPResponse, bkFirstErr(resp.JSON400, resp.JSON404, resp.JSON500), resp.Body)
				}
				return renderSDK(cmd, resp.JSON200.Data)
			}
			resp, err := client.PostApiV1BackupsStorageIdTestWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return bkSDKError(resp.HTTPResponse, bkFirstErr(resp.JSON400, resp.JSON404, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().BoolVar(&useTestConnection, "connection", false, "use the /test-connection variant instead of /test")
	return cmd
}

// ---------------------------------------------------------------------
// storage-configs: list / get / create / update / delete / test-connection
// ---------------------------------------------------------------------

func newBackupStorageConfigsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "storage-configs",
		Short: "Manage backup storage configs (the /storage-configs endpoints)",
	}
	cmd.AddCommand(
		newStorageConfigsListCmd(),
		newStorageConfigsGetCmd(),
		newStorageConfigsCreateCmd(),
		newStorageConfigsUpdateCmd(),
		newStorageConfigsDeleteCmd(),
		newStorageConfigsTestConnCmd(),
	)
	return cmd
}

func newStorageConfigsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List storage configs",
		Args:  cobra.NoArgs,
	}
	limit, offset := bkLimitOffsetFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		client, err := newAstroClient(cmd)
		if err != nil {
			return err
		}
		params := &astroclient.GetApiV1BackupsStorageConfigsParams{
			Limit:  bkPageParam(cmd, "limit", limit),
			Offset: bkPageParam(cmd, "offset", offset),
		}
		resp, err := client.GetApiV1BackupsStorageConfigsWithResponse(cmd.Context(), params)
		if err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return bkSDKError(resp.HTTPResponse, resp.JSON500, resp.Body)
		}
		return renderSDK(cmd, resp.JSON200.Data)
	}
	return cmd
}

func newStorageConfigsGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one storage config",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := bkParseUUID(args[0])
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1BackupsStorageConfigsIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return bkSDKError(resp.HTTPResponse, bkFirstErr(resp.JSON400, resp.JSON404), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

func newStorageConfigsCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a storage config",
		Args:  cobra.NoArgs,
	}
	build := storageConfigFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		client, err := newAstroClient(cmd)
		if err != nil {
			return err
		}
		body, err := build()
		if err != nil {
			return err
		}
		resp, err := client.PostApiV1BackupsStorageConfigsWithResponse(cmd.Context(), body)
		if err != nil {
			return err
		}
		if resp.JSON201 == nil {
			return bkSDKError(resp.HTTPResponse, bkFirstErr(resp.JSON400, resp.JSON500), resp.Body)
		}
		return renderSDK(cmd, resp.JSON201.Data)
	}
	return cmd
}

func newStorageConfigsUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a storage config (full replacement)",
		Args:  cobra.ExactArgs(1),
	}
	build := storageConfigFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		client, err := newAstroClient(cmd)
		if err != nil {
			return err
		}
		id, err := bkParseUUID(args[0])
		if err != nil {
			return err
		}
		body, err := build()
		if err != nil {
			return err
		}
		resp, err := client.PutApiV1BackupsStorageConfigsIdWithResponse(cmd.Context(), id, body)
		if err != nil {
			return err
		}
		if resp.JSON200 == nil {
			return bkSDKError(resp.HTTPResponse, bkFirstErr(resp.JSON400, resp.JSON500), resp.Body)
		}
		return renderSDK(cmd, resp.JSON200.Data)
	}
	return cmd
}

func newStorageConfigsDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a storage config",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := bkParseUUID(args[0])
			if err != nil {
				return err
			}
			resp, err := client.DeleteApiV1BackupsStorageConfigsIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if !bkOKStatus(resp.HTTPResponse) {
				return bkSDKError(resp.HTTPResponse, bkFirstErr(resp.JSON400, resp.JSON500), resp.Body)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted storage config %s.\n", args[0])
			return err
		},
	}
	return cmd
}

func newStorageConfigsTestConnCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "test-connection <id>",
		Aliases: []string{"test"},
		Short:   "Test a storage config's connectivity",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			id, err := bkParseUUID(args[0])
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1BackupsStorageConfigsIdTestConnectionWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return bkSDKError(resp.HTTPResponse, bkFirstErr(resp.JSON400, resp.JSON404, resp.JSON500), resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// controller-status
// ---------------------------------------------------------------------

func newBackupControllerStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "controller-status",
		Short: "Show the backup controller status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1BackupsControllerStatusWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return bkSDKError(resp.HTTPResponse, resp.JSON500, resp.Body)
			}
			return renderSDK(cmd, resp.JSON200.Data)
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// small response helpers
// ---------------------------------------------------------------------

// bkFirstErr returns the first non-nil typed ErrorResponse from the given
// candidates, so a single bkSDKError call can cover a method's several error
// status codes.
func bkFirstErr(candidates ...*astroclient.ErrorResponse) *astroclient.ErrorResponse {
	for _, c := range candidates {
		if c != nil {
			return c
		}
	}
	return nil
}

// bkOKStatus reports whether the HTTP response carried a 2xx status. Delete
// endpoints return no typed body, so success is judged by status alone.
func bkOKStatus(resp *http.Response) bool {
	return resp != nil && resp.StatusCode >= 200 && resp.StatusCode < 300
}
