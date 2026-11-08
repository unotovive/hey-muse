package echod

import (
	"errors"
	"fmt"

	"github.com/HuskerMinion/techo5/echod/internal/lib/standalone"
	"github.com/spf13/cobra"
)

// Read through the same Go entry points as the standby screen without actuating devices.
func newHomeCheckCmd() *cobra.Command {
	return &cobra.Command{Use: "home-check", Short: "Check the native calendar and home appliance connection", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			h := standalone.Device
			name, err := h.BackendName()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Backend: %s\n", name)
			agenda, err := h.Dashboard(cmd.Context(), "agenda", "")
			if err != nil {
				return err
			}
			if !agenda.OK || agenda.Stale {
				return errors.New("calendar refresh unavailable")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Calendar refreshed: %d events, %s\n", len(agenda.Events), agenda.Fetched.Format("2006-01-02 15:04:05 MST"))
			env, err := h.Dashboard(cmd.Context(), "environment", "")
			if err != nil {
				return err
			}
			if !env.OK {
				return errors.New("sensor readings unavailable")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Sensor readings: temperature=%t humidity=%t CO2=%t battery=%t\n", env.Environment.Temperature != nil, env.Environment.Humidity != nil, env.Environment.CO2 != nil, env.Environment.Battery != nil)
			controls, err := h.Dashboard(cmd.Context(), "power_status", "")
			if err != nil {
				return err
			}
			for _, c := range controls.Controls {
				fmt.Fprintln(cmd.OutOrStdout(), c.ID)
			}
			n, err := h.CheckTargets(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Voice targets resolved: %d\n", n)
			return nil
		}}
}
