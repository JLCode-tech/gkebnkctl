package phases

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/registry"
	"helm.sh/helm/v3/pkg/release"
)

const (
	FLOReleaseName            = "f5-lifecycle-operator"
	FLONamespace              = "f5-cne-core"
	FLOChartRef               = "oci://repo.f5.com/charts/f5-lifecycle-operator"
	floRegistryHost           = "repo.f5.com"
	FLOValuesTemplatePath     = "templates/flo-values.yaml.tmpl"
	CNEControllerRBACYAMLPath = "templates/cne-controller-rbac.yaml.tmpl"
	ServiceCIDRStateKey       = "SERVICE_CIDR"
	floHelmInstallTimeout     = 15 * time.Minute
)

// ExtractFARPayload unpacks a .tgz archive if compressed, returning the underlying key content.
func ExtractFARPayload(data []byte) []byte {
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err == nil {
			defer gz.Close()
			tr := tar.NewReader(gz)
			for {
				hdr, err := tr.Next()
				if err != nil {
					break
				}
				if hdr.Typeflag == tar.TypeReg {
					content, err := io.ReadAll(tr)
					if err == nil && len(content) > 0 {
						return content
					}
				}
			}
		}
	}
	return data
}

// HelmInstaller is the Helm SDK surface phase 32 and bnk upgrade share.
type HelmInstaller interface {
	List(namespace, filter string) ([]*release.Release, error)
	Install(releaseName, namespace string, chart *chart.Chart, values map[string]interface{}) (*release.Release, error)
	Upgrade(releaseName, namespace string, chart *chart.Chart, values map[string]interface{}) (*release.Release, error)
	Uninstall(releaseName, namespace string) error
	PullAndLoad(chartRef, version string) (*chart.Chart, error)
}

type realHelmInstaller struct {
	actionConfig *action.Configuration
	settings     *cli.EnvSettings
}

func (r *realHelmInstaller) List(namespace, filter string) ([]*release.Release, error) {
	l := action.NewList(r.actionConfig)
	l.Filter = filter
	l.AllNamespaces = false
	l.SetStateMask()
	return l.Run()
}

func (r *realHelmInstaller) Install(releaseName, namespace string, ch *chart.Chart, values map[string]interface{}) (*release.Release, error) {
	inst := action.NewInstall(r.actionConfig)
	inst.ReleaseName = releaseName
	inst.Namespace = namespace
	inst.Wait = true
	inst.Timeout = floHelmInstallTimeout
	inst.CreateNamespace = false
	return inst.Run(ch, values)
}

func (r *realHelmInstaller) Upgrade(releaseName, namespace string, ch *chart.Chart, values map[string]interface{}) (*release.Release, error) {
	upg := action.NewUpgrade(r.actionConfig)
	upg.Namespace = namespace
	upg.Wait = true
	upg.Timeout = floHelmInstallTimeout
	return upg.Run(releaseName, ch, values)
}

func (r *realHelmInstaller) Uninstall(releaseName, _ string) error {
	uns := action.NewUninstall(r.actionConfig)
	uns.IgnoreNotFound = true
	_, err := uns.Run(releaseName)
	return err
}

func (r *realHelmInstaller) PullAndLoad(chartRef, version string) (*chart.Chart, error) {
	tmpDir, err := os.MkdirTemp("", "flo-chart-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir for chart pull: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	pull := action.NewPullWithOpts(action.WithConfig(r.actionConfig))
	pull.Settings = r.settings
	pull.Version = version
	pull.DestDir = tmpDir
	pull.Untar = true
	if _, err := pull.Run(chartRef); err != nil {
		return nil, fmt.Errorf("pull chart %s@%s: %w", chartRef, version, err)
	}

	chartDir := tmpDir + "/f5-lifecycle-operator"
	ch, err := loader.Load(chartDir)
	if err != nil {
		return nil, fmt.Errorf("load chart from %s: %w", chartDir, err)
	}
	return ch, nil
}

// NewHelmInstaller logs in to repo.f5.com with the FAR key and returns a
// HelmInstaller bound to the FLO namespace.
func NewHelmInstaller(kubeconfigPath, farKeyB64 string) (HelmInstaller, error) {
	regClient, err := registry.NewClient()
	if err != nil {
		return nil, fmt.Errorf("create helm registry client: %w", err)
	}

	if farKeyB64 != "" {
		if err := regClient.Login(floRegistryHost,
			registry.LoginOptBasicAuth("_json_key_base64", farKeyB64),
		); err != nil {
			return nil, fmt.Errorf("helm registry login %s: %w", floRegistryHost, err)
		}
	}

	settings := cli.New()
	if kubeconfigPath != "" {
		settings.KubeConfig = kubeconfigPath
	}

	actionConfig := new(action.Configuration)
	logFn := func(format string, v ...interface{}) {
		fmt.Fprintf(os.Stderr, "[phases][helm] "+format+"\n", v...)
	}
	if err := actionConfig.Init(settings.RESTClientGetter(), FLONamespace, "secret", logFn); err != nil {
		return nil, fmt.Errorf("init helm action config: %w", err)
	}
	actionConfig.RegistryClient = regClient

	return &realHelmInstaller{
		actionConfig: actionConfig,
		settings:     settings,
	}, nil
}
