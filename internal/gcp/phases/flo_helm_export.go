package phases

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"

	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
)

const (
	FLOReleaseName            = k8s.FLOReleaseName
	FLONamespace              = k8s.FLONamespace
	FLOChartRef               = k8s.FLOChartRef
	FLOValuesTemplatePath     = "templates/flo-values.yaml.tmpl"
	CNEControllerRBACYAMLPath = "templates/cne-controller-rbac.yaml.tmpl"
	ServiceCIDRStateKey       = "SERVICE_CIDR"
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

// HelmInstaller is the Helm SDK surface phase 36 and bnk upgrade share.
type HelmInstaller = k8s.HelmInstaller

// NewHelmInstaller logs in to repo.f5.com with the FAR key and returns a
// HelmInstaller bound to the FLO namespace.
func NewHelmInstaller(kubeconfigPath, farKeyB64 string) (HelmInstaller, error) {
	return k8s.NewHelmInstaller(kubeconfigPath, farKeyB64)
}
