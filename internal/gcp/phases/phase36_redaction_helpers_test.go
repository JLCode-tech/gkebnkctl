package phases

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/manifests"
)

func manifestsFSReadFile(p string) ([]byte, error) { return manifests.FS.ReadFile(p) }

func parseYAMLStreamForTest(b []byte) ([]*unstructured.Unstructured, error) {
	return k8s.ParseYAMLStream(b)
}
