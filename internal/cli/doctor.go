package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/gkebnkctl/internal/doctor"
)

var (
	flagDoctorProject string
	flagDoctorRegion  string
	flagDoctorStrict  bool
	flagDoctorBackend string
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Verify host tooling (gcloud, kubectl), GCP ADC auth, APIs, and project quotas",
	Long: `gkebnkctl doctor checks your environment before provisioning:
  - Required CLI tools on PATH (gcloud, kubectl)
  - Valid Application Default Credentials (ADC)
  - Target GCP project resolution
  - Required GCP APIs enabled (compute, container, iam)
  - Regional CPU and IP address quotas

Example:
  gkebnkctl doctor
  gkebnkctl doctor --project my-gcp-project --region us-central1 --strict
  gkebnkctl doctor --backend k8s`,
	RunE: runDoctor,
}

func init() {
	doctorCmd.Flags().StringVar(&flagDoctorProject, "project", "", "GCP project ID to verify")
	doctorCmd.Flags().StringVar(&flagDoctorRegion, "region", "", "GCP region to check quotas for (e.g. us-central1)")
	doctorCmd.Flags().BoolVar(&flagDoctorStrict, "strict", false, "Fail with non-zero exit if any warnings are raised")
	doctorCmd.Flags().StringVar(&flagDoctorBackend, "backend", "", "Run doctor checks against a specific backend (e.g. k8s)")
	rootCmd.AddCommand(doctorCmd)
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	out := os.Stdout

	fmt.Fprintln(out, "gkebnkctl doctor: verifying environment & prerequisites...")
	fmt.Fprintln(out)

	cfg := doctor.Config{
		Project: flagDoctorProject,
		Region:  flagDoctorRegion,
		Strict:  flagDoctorStrict,
	}

	results := doctor.Run(ctx, cfg)
	if flagDoctorBackend != "" {
		results = append(results, runBackendChecks(ctx, flagDoctorBackend)...)
	}
	if err := doctor.PrintResults(out, results); err != nil {
		return err
	}

	fmt.Fprintln(out)
	if doctor.HasFailures(results) {
		return fmt.Errorf("doctor detected failed prerequisites — see commands above to resolve")
	}

	if flagDoctorStrict && doctor.HasWarnings(results) {
		return fmt.Errorf("doctor detected warnings with --strict enabled")
	}

	fmt.Fprintln(out, "[ok] Environment ready for gkebnkctl provisioning.")
	return nil
}
