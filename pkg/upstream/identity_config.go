package upstream

import (
	"io/ioutil"
	"path/filepath"

	"github.com/pkg/errors"
	kotsv1beta1 "github.com/replicatedhq/kots/kotskinds/apis/kots/v1beta1"
	"k8s.io/client-go/kubernetes/scheme"
)

func LoadIdentityConfig(upstreamDir string) (*kotsv1beta1.IdentityConfig, error) {
	content, err := ioutil.ReadFile(filepath.Join(upstreamDir, "userdata", "identityconfig.yaml"))
	if err != nil {
		return nil, errors.Wrap(err, "failed to read existing identity Config")
	}

	decode := scheme.Codecs.UniversalDeserializer().Decode
	obj, gvk, err := decode(content, nil, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode identity Config")
	}

	if gvk.Group == "kots.io" && gvk.Version == "v1beta1" && gvk.Kind == "IdentityConfig" {
		return obj.(*kotsv1beta1.IdentityConfig), nil
	}

	return nil, errors.Errorf("unexpected gvk in identity Config file: %s/%s/%s", gvk.Group, gvk.Version, gvk.Kind)
}

func SaveIdentityConfig(identityConfig *kotsv1beta1.IdentityConfig, upstreamDir string) error {
	filename := filepath.Join(upstreamDir, "userdata", "identityconfig.yaml")
	err := ioutil.WriteFile(filename, mustMarshalIdentityConfig(identityConfig), 0644)
	if err != nil {
		return errors.Wrap(err, "failed to write identity config")
	}
	return nil
}
