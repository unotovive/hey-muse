package echod

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/HuskerMinion/techo5/echod/internal/feature/quake"
)

// newQuakeTestCmd puts a sample earthquake page on the running device's screen, to see it without
// waiting for an earthquake. The tool is a process of its own, so it leaves the code where the running
// echod looks for it (quake.TestRequestPath); echod shows quake.Sample(code) within a couple of seconds,
// whether or not the earthquake page is turned on.
func newQuakeTestCmd() *cobra.Command {
	var code int
	c := &cobra.Command{
		Use:   "quake-test",
		Short: "Show a sample earthquake alert (551, 552 or 556) on the running device's screen",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !quake.SampleCode(code) {
				return fmt.Errorf("--code must be 551, 552 or 556, not %d", code)
			}
			a := quake.Sample(code)
			if err := os.WriteFile(quake.TestRequestPath, []byte(strconv.Itoa(code)+"\n"), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Asked echod to show sample %d (%s): it comes up within seconds and goes after 90 s (60 s for 556) or a tap\n",
				code, a.Place+join(a.Prefs))
			return nil
		},
	}
	c.Flags().IntVar(&code, "code", 551, "the message's code: 551 earthquake, 552 tsunami, 556 early warning")
	return c
}

func join(prefs []string) string {
	if len(prefs) == 0 {
		return ""
	}
	return " " + fmt.Sprint(prefs)
}
