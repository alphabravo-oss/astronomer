// User subcommands: list / get / create / update / delete / reset-password
// plus the admin account actions (unlock / force-logout / disable-totp).
//
// This group is backed by the generated pkg/astroclient SDK rather than the
// legacy astrocli.Client. Each command builds the typed client via
// newAstroClient (client.go), calls the matching generated method, and
// renders the typed JSON body through renderSDK so the global -o/--output
// flag (table|json|yaml) is honored uniformly. Errors are mapped to a clean
// message; cobra surfaces a non-zero exit for any returned error.
//
// Style mirrors cluster.go: one constructor per subcommand, flags declared
// locally, and a small set of shared helpers at the bottom.

package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/spf13/cobra"

	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
)

func newUsersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "users",
		Aliases: []string{"user"},
		Short:   "Manage users",
	}
	cmd.AddCommand(
		newUsersListCmd(),
		newUsersGetCmd(),
		newUsersCreateCmd(),
		newUsersUpdateCmd(),
		newUsersDeleteCmd(),
		newUsersResetPasswordCmd(),
		newUsersAdminCmd(),
	)
	return cmd
}

// ---------------------------------------------------------------------
// list
// ---------------------------------------------------------------------

func newUsersListCmd() *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List users",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			params := &astroclient.GetApiV1UsersParams{}
			if cmd.Flags().Changed("limit") {
				params.Limit = &limit
			}
			if cmd.Flags().Changed("offset") {
				params.Offset = &offset
			}
			resp, err := client.GetApiV1UsersWithResponse(cmd.Context(), params)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return usersSDKError(resp.StatusCode(), resp.Body)
			}
			rows := resp.JSON200.Data
			return render(cmd, rows, func(w io.Writer) error {
				return writeUserTable(w, rows)
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "max number of users to return")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset")
	return cmd
}

// ---------------------------------------------------------------------
// get
// ---------------------------------------------------------------------

func newUsersGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one user's details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUserID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.GetApiV1UsersIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return usersSDKErrorEnvelope(resp.StatusCode(), resp.JSON404, resp.Body)
			}
			user := resp.JSON200.Data
			return render(cmd, user, func(w io.Writer) error {
				return writeUserTable(w, []astroclient.UsersSettingsUserListItem{user})
			})
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// create
// ---------------------------------------------------------------------

func newUsersCreateCmd() *cobra.Command {
	var (
		email, password, username, firstName, lastName string
		isActive, isStaff, isSuperuser                 bool
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new user",
		Long: `Creates a user. --email and --password are required; --username
defaults to the email when omitted, and --active defaults to true.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(email) == "" {
				return fmt.Errorf("--email is required")
			}
			if password == "" {
				return fmt.Errorf("--password is required")
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			body := astroclient.PostApiV1UsersJSONRequestBody{
				Email:    openapi_types.Email(email),
				Password: password,
			}
			if cmd.Flags().Changed("username") {
				body.Username = &username
			}
			if cmd.Flags().Changed("first-name") {
				body.FirstName = &firstName
			}
			if cmd.Flags().Changed("last-name") {
				body.LastName = &lastName
			}
			if cmd.Flags().Changed("active") {
				body.IsActive = &isActive
			}
			if cmd.Flags().Changed("staff") {
				body.IsStaff = &isStaff
			}
			if cmd.Flags().Changed("superuser") {
				body.IsSuperuser = &isSuperuser
			}
			resp, err := client.PostApiV1UsersWithResponse(cmd.Context(), body)
			if err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return usersSDKErrorEnvelope(resp.StatusCode(), resp.JSON400, resp.Body)
			}
			user := resp.JSON201.Data
			return render(cmd, user, func(w io.Writer) error {
				return writeUserTable(w, []astroclient.UsersSettingsUserListItem{user})
			})
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "email address (required)")
	cmd.Flags().StringVar(&password, "password", "", "initial password (required)")
	cmd.Flags().StringVar(&username, "username", "", "username (defaults to email)")
	cmd.Flags().StringVar(&firstName, "first-name", "", "first name")
	cmd.Flags().StringVar(&lastName, "last-name", "", "last name")
	cmd.Flags().BoolVar(&isActive, "active", true, "whether the account is active")
	cmd.Flags().BoolVar(&isStaff, "staff", false, "grant staff (admin UI) access")
	cmd.Flags().BoolVar(&isSuperuser, "superuser", false, "grant superuser privileges")
	return cmd
}

// ---------------------------------------------------------------------
// update — PATCH (partial). PUT is exposed via --replace.
// ---------------------------------------------------------------------

func newUsersUpdateCmd() *cobra.Command {
	var (
		email, username, firstName, lastName string
		isActive                             bool
		replace                              bool
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a user (PATCH by default; --replace uses PUT)",
		Long: `Updates a user's profile fields. Only the flags you set are sent.

By default this issues a PATCH (partial update). Pass --replace to issue a
PUT instead; both map to the same request shape server-side.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUserID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			body := astroclient.UsersSettingsUpdateUserRequest{}
			set := false
			if cmd.Flags().Changed("email") {
				e := openapi_types.Email(email)
				body.Email = &e
				set = true
			}
			if cmd.Flags().Changed("username") {
				body.Username = &username
				set = true
			}
			if cmd.Flags().Changed("first-name") {
				body.FirstName = &firstName
				set = true
			}
			if cmd.Flags().Changed("last-name") {
				body.LastName = &lastName
				set = true
			}
			if cmd.Flags().Changed("active") {
				body.IsActive = &isActive
				set = true
			}
			if !set {
				return fmt.Errorf("nothing to update: set at least one of --email/--username/--first-name/--last-name/--active")
			}

			if replace {
				resp, err := client.PutApiV1UsersIdWithResponse(cmd.Context(), id, body)
				if err != nil {
					return err
				}
				if resp.JSON200 == nil {
					return usersSDKErrorEnvelope(resp.StatusCode(), resp.JSON404, resp.Body)
				}
				user := resp.JSON200.Data
				return render(cmd, user, func(w io.Writer) error {
					return writeUserTable(w, []astroclient.UsersSettingsUserListItem{user})
				})
			}

			resp, err := client.PatchApiV1UsersIdWithResponse(cmd.Context(), id, body)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return usersSDKErrorEnvelope(resp.StatusCode(), resp.JSON404, resp.Body)
			}
			user := resp.JSON200.Data
			return render(cmd, user, func(w io.Writer) error {
				return writeUserTable(w, []astroclient.UsersSettingsUserListItem{user})
			})
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "new email address")
	cmd.Flags().StringVar(&username, "username", "", "new username")
	cmd.Flags().StringVar(&firstName, "first-name", "", "new first name")
	cmd.Flags().StringVar(&lastName, "last-name", "", "new last name")
	cmd.Flags().BoolVar(&isActive, "active", true, "set the account active flag")
	cmd.Flags().BoolVar(&replace, "replace", false, "use PUT instead of PATCH")
	return cmd
}

// ---------------------------------------------------------------------
// delete
// ---------------------------------------------------------------------

func newUsersDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a user",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUserID(args[0])
			if err != nil {
				return err
			}
			if !yes {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "About to delete user %s. Proceed? [y/N] ", args[0]); err != nil {
					return err
				}
				var resp string
				_, _ = fmt.Scanln(&resp)
				resp = strings.ToLower(strings.TrimSpace(resp))
				if resp != "y" && resp != "yes" {
					return fmt.Errorf("aborted")
				}
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.DeleteApiV1UsersIdWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if sc := resp.StatusCode(); sc < 200 || sc >= 300 {
				return usersSDKErrorEnvelope(sc, resp.JSON404, resp.Body)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted user %s.\n", args[0])
			return err
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// ---------------------------------------------------------------------
// reset-password
// ---------------------------------------------------------------------

func newUsersResetPasswordCmd() *cobra.Command {
	var password string
	cmd := &cobra.Command{
		Use:   "reset-password <id>",
		Short: "Reset a user's password",
		Long: `Resets a user's password. Provide --password to set an explicit
value, or omit it to have the server auto-generate a temporary password
(returned exactly once in the response).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUserID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			body := astroclient.PostApiV1UsersIdResetPasswordJSONRequestBody{}
			if cmd.Flags().Changed("password") {
				body.Password = &password
			}
			resp, err := client.PostApiV1UsersIdResetPasswordWithResponse(cmd.Context(), id, body)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return usersSDKErrorEnvelope(resp.StatusCode(), resp.JSON404, resp.Body)
			}
			return renderUserBody(cmd, resp.JSON200.Data)
		},
	}
	cmd.Flags().StringVar(&password, "password", "", "explicit new password (auto-generated when omitted)")
	return cmd
}

// ---------------------------------------------------------------------
// admin subcommands
// ---------------------------------------------------------------------

func newUsersAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Privileged account actions",
	}
	cmd.AddCommand(
		newUsersAdminUnlockCmd(),
		newUsersAdminForceLogoutCmd(),
		newUsersAdminDisableTotpCmd(),
	)
	return cmd
}

func newUsersAdminUnlockCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unlock <id>",
		Short: "Clear a user's account lockout",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUserID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1AdminUsersIdUnlockWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return usersSDKErrorEnvelope(resp.StatusCode(), resp.JSON404, resp.Body)
			}
			return renderUserBody(cmd, resp.JSON200)
		},
	}
	return cmd
}

func newUsersAdminForceLogoutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "force-logout <id>",
		Short: "Invalidate all of a user's active sessions/tokens",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUserID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1AdminUsersIdForceLogoutWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return usersSDKErrorEnvelope(resp.StatusCode(), resp.JSON404, resp.Body)
			}
			return renderUserBody(cmd, resp.JSON200)
		},
	}
	return cmd
}

func newUsersAdminDisableTotpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disable-totp <id>",
		Short: "Disable a user's TOTP (two-factor) enrollment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseUserID(args[0])
			if err != nil {
				return err
			}
			client, err := newAstroClient(cmd)
			if err != nil {
				return err
			}
			resp, err := client.PostApiV1AdminUsersIdDisableTotpWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return usersSDKErrorEnvelope(resp.StatusCode(), resp.JSON404, resp.Body)
			}
			return renderUserBody(cmd, resp.JSON200)
		},
	}
	return cmd
}

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

// parseUserID validates the positional id argument as a UUID so we fail
// fast with a clean message instead of sending a malformed request.
func parseUserID(s string) (openapi_types.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return openapi_types.UUID{}, fmt.Errorf("invalid user id %q: must be a UUID", s)
	}
	return id, nil
}

// renderUserBody renders a typed SDK payload. The generic table renderer in
// renderSDK only understands []any / map[string]any, so for typed structs
// (which is what these endpoints return) we route table output through the
// json fallback by passing a nil tableFn — json/yaml stay lossless and the
// default format still prints the object readably.
func renderUserBody(cmd *cobra.Command, body any) error {
	return render(cmd, body, nil)
}

// writeUserTable renders user list items as a compact column table.
func writeUserTable(w io.Writer, rows []astroclient.UsersSettingsUserListItem) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ID\tUSERNAME\tEMAIL\tENABLED\tPROVIDER\tROLES\tLAST LOGIN"); err != nil {
		return err
	}
	for _, r := range rows {
		id := "—"
		if r.Id != nil {
			id = truncate(r.Id.String(), 8)
		}
		email := ""
		if r.Email != nil {
			email = string(*r.Email)
		}
		enabled := "—"
		if r.Enabled != nil {
			if *r.Enabled {
				enabled = "yes"
			} else {
				enabled = "no"
			}
		}
		roles := ""
		if r.GlobalRoles != nil {
			roles = strings.Join(*r.GlobalRoles, ",")
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			id,
			defaultStr(derefStr(r.Username), "—"),
			defaultStr(email, "—"),
			enabled,
			defaultStr(derefStr(r.Provider), "—"),
			defaultStr(roles, "—"),
			defaultStr(derefStr(r.LastLogin), "—"),
		); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// usersSDKErrorEnvelope maps a non-2xx SDK response to a clean error. It prefers
// the typed ErrorEnvelope (code + message) when the server returned one,
// otherwise falls back to the raw body / status code.
func usersSDKErrorEnvelope(status int, env *astroclient.ErrorEnvelope, body []byte) error {
	if env != nil && env.Error.Message != "" {
		if env.Error.Code != "" {
			return fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
		}
		return fmt.Errorf("%s", env.Error.Message)
	}
	return usersSDKError(status, body)
}

// usersSDKError builds a generic error from a status code and raw body.
func usersSDKError(status int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = http.StatusText(status)
	}
	return fmt.Errorf("request failed (HTTP %d): %s", status, msg)
}
