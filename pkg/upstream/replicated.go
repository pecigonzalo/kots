package upstream

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	kotsv1beta1 "github.com/replicatedhq/kots/kotskinds/apis/kots/v1beta1"
	reportingtypes "github.com/replicatedhq/kots/pkg/api/reporting/types"
	"github.com/replicatedhq/kots/pkg/buildversion"
	kotslicense "github.com/replicatedhq/kots/pkg/license"
	reporting "github.com/replicatedhq/kots/pkg/reporting"
	"github.com/replicatedhq/kots/pkg/template"
	"github.com/replicatedhq/kots/pkg/upstream/types"
	"github.com/replicatedhq/kots/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	serializer "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/client-go/kubernetes/scheme"
)

const DefaultMetadata = `apiVersion: kots.io/v1beta1
kind: Application
metadata:
  name: "default-application"
spec:
  title: "the application"
  icon: https://cdn2.iconfinder.com/data/icons/mixd/512/16_kubernetes-512.png
  releaseNotes: |
    release notes`

type ReplicatedUpstream struct {
	Channel      *string
	AppSlug      string
	VersionLabel *string
	Sequence     *int
}

type ReplicatedCursor struct {
	ChannelID   string
	ChannelName string
	Cursor      string
}

type App struct {
	Name string
}

type Release struct {
	UpdateCursor ReplicatedCursor
	VersionLabel string
	ReleaseNotes string
	ReleasedAt   *time.Time
	Manifests    map[string][]byte
}

type ChannelRelease struct {
	ChannelSequence int    `json:"channelSequence"`
	ReleaseSequence int    `json:"releaseSequence"`
	VersionLabel    string `json:"versionLabel"`
}

func (this ReplicatedCursor) Equal(other ReplicatedCursor) bool {
	if this.ChannelID != "" && other.ChannelID != "" {
		return this.ChannelID == other.ChannelID && this.Cursor == other.Cursor
	}
	return this.ChannelName == other.ChannelName && this.Cursor == other.Cursor
}

func getUpdatesReplicated(u *url.URL, fetchOptions *types.FetchOptions) ([]types.Update, error) {
	currentCursor := ReplicatedCursor{
		ChannelID:   fetchOptions.CurrentChannelID,
		ChannelName: fetchOptions.CurrentChannelName,
		Cursor:      fetchOptions.CurrentCursor,
	}

	if fetchOptions.LocalPath != "" {
		parsedLocalRelease, err := readReplicatedAppFromLocalPath(fetchOptions.LocalPath, currentCursor, fetchOptions.CurrentVersionLabel)
		if err != nil {
			return nil, errors.Wrap(err, "failed to read replicated app from local path")
		}

		return []types.Update{{Cursor: parsedLocalRelease.UpdateCursor.Cursor, VersionLabel: fetchOptions.CurrentVersionLabel}}, nil
	}

	// A license file is required to be set for this to succeed
	if fetchOptions.License == nil {
		return nil, errors.New("No license was provided")
	}

	replicatedUpstream, err := parseReplicatedURL(u)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse replicated upstream")
	}

	if err := getSuccessfulHeadResponse(replicatedUpstream, fetchOptions.License); err != nil {
		return nil, errors.Wrap(err, "failed to get successful head response")
	}

	pendingReleases, err := listPendingChannelReleases(replicatedUpstream, fetchOptions.License, fetchOptions.LastUpdateCheckAt, currentCursor, fetchOptions.ChannelChanged, fetchOptions.ReportingInfo)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list replicated app releases")
	}

	updates := []types.Update{}
	for _, pendingRelease := range pendingReleases {
		updates = append(updates, types.Update{
			Cursor:       strconv.Itoa(pendingRelease.ChannelSequence),
			VersionLabel: pendingRelease.VersionLabel,
		})
	}
	return updates, nil
}

func downloadReplicated(
	u *url.URL,
	localPath string,
	rootDir string,
	useAppDir bool,
	license *kotsv1beta1.License,
	existingConfigValues *kotsv1beta1.ConfigValues,
	existingIdentityConfig *kotsv1beta1.IdentityConfig,
	updateCursor ReplicatedCursor,
	versionLabel string,
	appSlug string,
	appSequence int64,
	isAirgap bool,
	airgapMetadata *kotsv1beta1.Airgap,
	registry types.LocalRegistry,
	reportingInfo *reportingtypes.ReportingInfo,
) (*types.Upstream, error) {
	var release *Release

	if localPath != "" {
		parsedLocalRelease, err := readReplicatedAppFromLocalPath(localPath, updateCursor, versionLabel)
		if err != nil {
			return nil, errors.Wrap(err, "failed to read replicated app from local path")
		}

		// airgapMetadata is nil when saving initial config
		if airgapMetadata != nil {
			parsedLocalRelease.ReleaseNotes = airgapMetadata.Spec.ReleaseNotes
		}

		release = parsedLocalRelease
	} else {
		// A license file is required to be set for this to succeed
		if license == nil {
			return nil, errors.New("No license was provided")
		}

		replicatedUpstream, err := parseReplicatedURL(u)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse replicated upstream")
		}

		if err := getSuccessfulHeadResponse(replicatedUpstream, license); err != nil {
			return nil, errors.Wrap(err, "failed to get successful head response")
		}

		downloadedRelease, err := downloadReplicatedApp(replicatedUpstream, license, updateCursor, reportingInfo)
		if err != nil {
			return nil, errors.Wrap(err, "failed to download replicated app")
		}

		licenseData, err := kotslicense.GetLatestLicense(license)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get latest license")
		}
		license = licenseData.License

		release = downloadedRelease
	}

	// Find the config in the upstream and write out default values

	application := findAppInRelease(release) // this function never returns nil

	// NOTE: this currently comes from the application spec and not the channel release meta
	if release.ReleaseNotes == "" {
		release.ReleaseNotes = application.Spec.ReleaseNotes
	}

	// get channel name from license, if one was provided
	channelID, channelName := "", ""
	if license != nil {
		channelID = license.Spec.ChannelID
		channelName = license.Spec.ChannelName
	}

	if existingIdentityConfig == nil {
		var prevIdentityConfigFile string
		if useAppDir {
			prevIdentityConfigFile = filepath.Join(rootDir, application.Name, "upstream", "userdata", "identityconfig.yaml")
		} else {
			prevIdentityConfigFile = filepath.Join(rootDir, "upstream", "userdata", "identityconfig.yaml")
		}
		var err error
		existingIdentityConfig, err = findIdentityConfigInFile(prevIdentityConfigFile)
		if err != nil {
			return nil, errors.Wrap(err, "failed to load existing identity config")
		}
	}

	if existingIdentityConfig != nil {
		release.Manifests["userdata/identityconfig.yaml"] = mustMarshalIdentityConfig(existingIdentityConfig)
	}

	if existingConfigValues == nil {
		var prevConfigFile string
		if useAppDir {
			prevConfigFile = filepath.Join(rootDir, application.Name, "upstream", "userdata", "config.yaml")
		} else {
			prevConfigFile = filepath.Join(rootDir, "upstream", "userdata", "config.yaml")
		}
		var err error
		existingConfigValues, err = findConfigValuesInFile(prevConfigFile)
		if err != nil {
			return nil, errors.Wrap(err, "failed to load existing config values")
		}
	}

	config, _, _, _, _, err := findTemplateContextDataInRelease(release)
	if err != nil {
		return nil, errors.Wrap(err, "failed to find config in release")
	}
	if config != nil || existingConfigValues != nil {
		appInfo := template.ApplicationInfo{
			Slug: appSlug,
		}

		versionInfo := template.VersionInfo{
			Sequence:     appSequence,
			Cursor:       updateCursor.Cursor,
			ChannelName:  channelName,
			VersionLabel: release.VersionLabel,
			ReleaseNotes: release.ReleaseNotes,
			IsAirgap:     isAirgap,
		}

		localRegistry := template.LocalRegistry{
			Host:      registry.Host,
			Namespace: registry.Namespace,
			Username:  registry.Username,
			Password:  registry.Password,
		}

		// If config existed and was removed from the app,
		// values will be carried over to the new version anyway.
		configValues, err := createConfigValues(application.Name, config, existingConfigValues, license, application, &appInfo, &versionInfo, localRegistry, existingIdentityConfig)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create empty config values")
		}

		release.Manifests["userdata/config.yaml"] = mustMarshalConfigValues(configValues)
	}

	// Add the license to the upstream, if one was provided
	if license != nil {
		release.Manifests["userdata/license.yaml"] = MustMarshalLicense(license)
	}

	files, err := releaseToFiles(release)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get files from release")
	}

	upstream := &types.Upstream{
		URI:          u.RequestURI(),
		Name:         application.Name,
		Files:        files,
		Type:         "replicated",
		UpdateCursor: release.UpdateCursor.Cursor,
		ChannelID:    channelID,
		ChannelName:  channelName,
		VersionLabel: release.VersionLabel,
		ReleaseNotes: release.ReleaseNotes,
		ReleasedAt:   release.ReleasedAt,
	}

	return upstream, nil
}

func (r *ReplicatedUpstream) getRequest(method string, license *kotsv1beta1.License, cursor ReplicatedCursor) (*http.Request, error) {
	u, err := url.Parse(license.Spec.Endpoint)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse endpoint from license")
	}

	hostname := u.Hostname()
	if u.Port() != "" {
		hostname = fmt.Sprintf("%s:%s", u.Hostname(), u.Port())
	}

	urlPath := path.Join(hostname, "release", license.Spec.AppSlug)
	if r.Channel != nil {
		urlPath = path.Join(urlPath, *r.Channel)
	}

	urlValues := url.Values{}
	urlValues.Set("channelSequence", cursor.Cursor)
	urlValues.Add("licenseSequence", fmt.Sprintf("%d", license.Spec.LicenseSequence))
	urlValues.Add("isSemverSupported", "true")

	url := fmt.Sprintf("%s://%s?%s", u.Scheme, urlPath, urlValues.Encode())

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to call newrequest")
	}

	req.Header.Add("User-Agent", fmt.Sprintf("KOTS/%s", buildversion.Version()))
	req.Header.Set("Authorization", fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", license.Spec.LicenseID, license.Spec.LicenseID)))))

	return req, nil
}

func parseReplicatedURL(u *url.URL) (*ReplicatedUpstream, error) {
	replicatedUpstream := ReplicatedUpstream{}

	if u.User != nil {
		if u.User.Username() != "" {
			replicatedUpstream.AppSlug = u.User.Username()
			versionLabel := u.Hostname()
			replicatedUpstream.VersionLabel = &versionLabel
		}
	}

	if replicatedUpstream.AppSlug == "" {
		replicatedUpstream.AppSlug = u.Hostname()
		if u.Path != "" {
			channel := strings.TrimPrefix(u.Path, "/")
			replicatedUpstream.Channel = &channel
		}
	}

	return &replicatedUpstream, nil
}

func getSuccessfulHeadResponse(replicatedUpstream *ReplicatedUpstream, license *kotsv1beta1.License) error {
	headReq, err := replicatedUpstream.getRequest("HEAD", license, ReplicatedCursor{})
	if err != nil {
		return errors.Wrap(err, "failed to create http request")
	}
	headResp, err := http.DefaultClient.Do(headReq)
	if err != nil {
		return errors.Wrap(err, "failed to execute head request")
	}
	defer headResp.Body.Close()

	if headResp.StatusCode == 401 {
		return errors.New("license was not accepted")
	}

	if headResp.StatusCode == 403 {
		return util.ActionableError{Message: "License is expired"}
	}

	if headResp.StatusCode >= 400 {
		return errors.Errorf("unexpected result from head request: %d", headResp.StatusCode)
	}

	return nil
}

func readReplicatedAppFromLocalPath(localPath string, localCursor ReplicatedCursor, versionLabel string) (*Release, error) {
	release := Release{
		Manifests:    make(map[string][]byte),
		UpdateCursor: localCursor,
		VersionLabel: versionLabel,
	}

	err := filepath.Walk(localPath,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if info.IsDir() {
				return nil
			}

			contents, err := ioutil.ReadFile(path)
			if err != nil {
				return err
			}

			// remove localpath prefix
			appPath := strings.TrimPrefix(path, localPath)
			appPath = strings.TrimLeft(appPath, string(os.PathSeparator))

			release.Manifests[appPath] = contents

			return nil
		})
	if err != nil {
		return nil, errors.Wrap(err, "failed to walk local path")
	}

	return &release, nil
}

func downloadReplicatedApp(replicatedUpstream *ReplicatedUpstream, license *kotsv1beta1.License, cursor ReplicatedCursor, reportingInfo *reportingtypes.ReportingInfo) (*Release, error) {
	getReq, err := replicatedUpstream.getRequest("GET", license, cursor)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create http request")
	}

	reporting.InjectReportingInfoHeaders(getReq, reportingInfo)

	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		return nil, errors.Wrap(err, "failed to execute get request")
	}
	defer getResp.Body.Close()

	if getResp.StatusCode >= 300 {
		body, _ := ioutil.ReadAll(getResp.Body)
		if len(body) > 0 {
			return nil, util.ActionableError{Message: string(body)}
		}
		return nil, errors.Errorf("unexpected result from get request: %d", getResp.StatusCode)
	}

	updateSequence := getResp.Header.Get("X-Replicated-ChannelSequence")
	updateChannelID := getResp.Header.Get("X-Replicated-ChannelID")
	updateChannelName := getResp.Header.Get("X-Replicated-ChannelName")
	versionLabel := getResp.Header.Get("X-Replicated-VersionLabel")
	releasedAtStr := getResp.Header.Get("X-Replicated-ReleasedAt")

	var releasedAt *time.Time
	r, err := time.Parse(time.RFC3339, releasedAtStr)
	if err == nil {
		releasedAt = &r
	}

	gzf, err := gzip.NewReader(getResp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create new gzip reader")
	}

	release := Release{
		Manifests: make(map[string][]byte),
		UpdateCursor: ReplicatedCursor{
			ChannelID:   updateChannelID,
			ChannelName: updateChannelName,
			Cursor:      updateSequence,
		},
		VersionLabel: versionLabel,
		ReleasedAt:   releasedAt,
		// NOTE: release notes come from Application spec
	}
	tarReader := tar.NewReader(gzf)
	i := 0
	for {
		header, err := tarReader.Next()

		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.Wrap(err, "failed to get next file from reader")
		}

		name := header.Name

		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
			content, err := ioutil.ReadAll(tarReader)
			if err != nil {
				return nil, errors.Wrap(err, "failed to read file from tar")
			}

			release.Manifests[name] = content
		}

		i++
	}

	return &release, nil
}

func listPendingChannelReleases(replicatedUpstream *ReplicatedUpstream, license *kotsv1beta1.License, lastUpdateCheckAt *time.Time, currentCursor ReplicatedCursor, channelChanged bool, reportingInfo *reportingtypes.ReportingInfo) ([]ChannelRelease, error) {
	u, err := url.Parse(license.Spec.Endpoint)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse endpoint from license")
	}

	hostname := u.Hostname()
	if u.Port() != "" {
		hostname = fmt.Sprintf("%s:%s", u.Hostname(), u.Port())
	}

	sequence := currentCursor.Cursor
	if channelChanged {
		sequence = ""
	}

	urlValues := url.Values{}
	urlValues.Set("channelSequence", sequence)
	urlValues.Add("licenseSequence", fmt.Sprintf("%d", license.Spec.LicenseSequence))
	urlValues.Add("isSemverSupported", "true")

	if lastUpdateCheckAt != nil {
		urlValues.Add("lastUpdateCheckAt", lastUpdateCheckAt.UTC().Format(time.RFC3339))
	}

	url := fmt.Sprintf("%s://%s/release/%s/pending?%s", u.Scheme, hostname, license.Spec.AppSlug, urlValues.Encode())

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to call newrequest")
	}

	reporting.InjectReportingInfoHeaders(req, reportingInfo)

	req.Header.Add("User-Agent", fmt.Sprintf("KOTS/%s", buildversion.Version()))
	req.Header.Set("Authorization", fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", license.Spec.LicenseID, license.Spec.LicenseID)))))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "failed to execute get request")
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read response body")
	}

	if resp.StatusCode >= 400 {
		if len(body) > 0 {
			return nil, util.ActionableError{Message: string(body)}
		}
		return nil, errors.Errorf("unexpected result from get request: %d", resp.StatusCode)
	}

	var channelReleases struct {
		ChannelReleases []ChannelRelease `json:"channelReleases"`
	}
	if err := json.Unmarshal(body, &channelReleases); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal response")
	}

	return channelReleases.ChannelReleases, nil
}

func MustMarshalLicense(license *kotsv1beta1.License) []byte {
	s := serializer.NewYAMLSerializer(serializer.DefaultMetaFactory, scheme.Scheme, scheme.Scheme)

	var b bytes.Buffer
	if err := s.Encode(license, &b); err != nil {
		panic(err)
	}

	return b.Bytes()
}

func mustMarshalConfigValues(configValues *kotsv1beta1.ConfigValues) []byte {
	s := serializer.NewYAMLSerializer(serializer.DefaultMetaFactory, scheme.Scheme, scheme.Scheme)

	var b bytes.Buffer
	if err := s.Encode(configValues, &b); err != nil {
		panic(err)
	}

	return b.Bytes()
}

func createConfigValues(applicationName string, config *kotsv1beta1.Config, existingConfigValues *kotsv1beta1.ConfigValues, license *kotsv1beta1.License, app *kotsv1beta1.Application, appInfo *template.ApplicationInfo, versionInfo *template.VersionInfo, localRegistry template.LocalRegistry, identityConfig *kotsv1beta1.IdentityConfig) (*kotsv1beta1.ConfigValues, error) {
	templateContextValues := make(map[string]template.ItemValue)

	var newValues kotsv1beta1.ConfigValuesSpec
	if existingConfigValues != nil {
		for k, v := range existingConfigValues.Spec.Values {
			value := v.Value
			if value == "" {
				value = v.ValuePlaintext
			}
			templateContextValues[k] = template.ItemValue{
				Value:    value,
				Default:  v.Default,
				Filename: v.Filename,
			}
		}
		newValues = kotsv1beta1.ConfigValuesSpec{
			Values: existingConfigValues.Spec.Values,
		}
	} else {
		newValues = kotsv1beta1.ConfigValuesSpec{
			Values: map[string]kotsv1beta1.ConfigValue{},
		}
	}

	if config == nil {
		return &kotsv1beta1.ConfigValues{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "kots.io/v1beta1",
				Kind:       "ConfigValues",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: applicationName,
			},
			Spec: newValues,
		}, nil
	}

	builderOptions := template.BuilderOptions{
		ConfigGroups:    config.Spec.Groups,
		ExistingValues:  templateContextValues,
		LocalRegistry:   localRegistry,
		License:         license,
		Application:     app,
		ApplicationInfo: appInfo,
		VersionInfo:     versionInfo,
		IdentityConfig:  identityConfig,
	}
	builder, _, err := template.NewBuilder(builderOptions)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create config context")
	}

	for _, group := range config.Spec.Groups {
		for _, item := range group.Items {
			var foundValue, foundValuePlaintext, foundFilename string
			prevValue, ok := newValues.Values[item.Name]
			if ok {
				foundValue = prevValue.Value
				foundValuePlaintext = prevValue.ValuePlaintext
				foundFilename = prevValue.Filename
			}

			renderedValue, err := builder.RenderTemplate(item.Name, item.Value.String())
			if err != nil {
				return nil, errors.Wrap(err, "failed to render config item value")
			}

			renderedDefault, err := builder.RenderTemplate(item.Name, item.Default.String())
			if err != nil {
				return nil, errors.Wrap(err, "failed to render config item default")
			}

			if foundValue != "" || foundValuePlaintext != "" {
				newValues.Values[item.Name] = kotsv1beta1.ConfigValue{
					Value:          foundValue,
					ValuePlaintext: foundValuePlaintext,
					Default:        renderedDefault,
					Filename:       foundFilename,
				}
			} else {
				newValues.Values[item.Name] = kotsv1beta1.ConfigValue{
					Value:    renderedValue,
					Default:  renderedDefault,
					Filename: foundFilename,
				}
				builderOptions.ExistingValues[item.Name] = template.ItemValue{
					Value:    renderedValue,
					Default:  renderedDefault,
					Filename: foundFilename,
				}
			}
		}
	}

	configValues := kotsv1beta1.ConfigValues{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "kots.io/v1beta1",
			Kind:       "ConfigValues",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: applicationName,
		},
		Spec: newValues,
	}

	return &configValues, nil
}

func findConfigValuesInFile(filename string) (*kotsv1beta1.ConfigValues, error) {
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, errors.Wrap(err, "failed to open file")
	}

	return contentToConfigValues(content), nil
}

func contentToConfigValues(content []byte) *kotsv1beta1.ConfigValues {
	decode := scheme.Codecs.UniversalDeserializer().Decode
	obj, gvk, err := decode(content, nil, nil)
	if err != nil {
		return nil
	}

	if gvk.Group == "kots.io" && gvk.Version == "v1beta1" && gvk.Kind == "ConfigValues" {
		return obj.(*kotsv1beta1.ConfigValues)
	}

	return nil
}

func mustMarshalIdentityConfig(identityConfig *kotsv1beta1.IdentityConfig) []byte {
	s := serializer.NewYAMLSerializer(serializer.DefaultMetaFactory, scheme.Scheme, scheme.Scheme)

	var b bytes.Buffer
	if err := s.Encode(identityConfig, &b); err != nil {
		panic(err)
	}

	return b.Bytes()
}

func findIdentityConfigInFile(filename string) (*kotsv1beta1.IdentityConfig, error) {
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, errors.Wrap(err, "failed to open file")
	}

	return contentToIdentityConfig(content), nil
}

func contentToIdentityConfig(content []byte) *kotsv1beta1.IdentityConfig {
	decode := scheme.Codecs.UniversalDeserializer().Decode
	obj, gvk, err := decode(content, nil, nil)
	if err != nil {
		return nil
	}

	if gvk.Group == "kots.io" && gvk.Version == "v1beta1" && gvk.Kind == "IdentityConfig" {
		return obj.(*kotsv1beta1.IdentityConfig)
	}

	return nil
}

func findTemplateContextDataInRelease(release *Release) (*kotsv1beta1.Config, *kotsv1beta1.ConfigValues, *kotsv1beta1.License, *kotsv1beta1.Installation, *kotsv1beta1.IdentityConfig, error) {
	var config *kotsv1beta1.Config
	var values *kotsv1beta1.ConfigValues
	var license *kotsv1beta1.License
	var installation *kotsv1beta1.Installation
	var identityConfig *kotsv1beta1.IdentityConfig

	for _, content := range release.Manifests {
		decode := scheme.Codecs.UniversalDeserializer().Decode
		obj, gvk, err := decode(content, nil, nil)
		if err != nil {
			continue
		}

		if gvk.Group == "kots.io" {
			if gvk.Version == "v1beta1" {
				if gvk.Kind == "Config" {
					config = obj.(*kotsv1beta1.Config)
				} else if gvk.Kind == "ConfigValues" {
					values = obj.(*kotsv1beta1.ConfigValues)
				} else if gvk.Kind == "License" {
					license = obj.(*kotsv1beta1.License)
				} else if gvk.Kind == "Installation" {
					installation = obj.(*kotsv1beta1.Installation)
				} else if gvk.Kind == "IdentityConfig" {
					identityConfig = obj.(*kotsv1beta1.IdentityConfig)
				}
			}
		}
	}

	return config, values, license, installation, identityConfig, nil
}

func findAppInRelease(release *Release) *kotsv1beta1.Application {
	for _, content := range release.Manifests {
		decode := scheme.Codecs.UniversalDeserializer().Decode
		obj, gvk, err := decode(content, nil, nil)
		if err != nil {
			continue
		}

		if gvk.Group == "kots.io" {
			if gvk.Version == "v1beta1" {
				if gvk.Kind == "Application" {
					return obj.(*kotsv1beta1.Application)
				}
			}
		}
	}

	// Using Ship apps for now, so let's create an app manifest on the fly
	app := &kotsv1beta1.Application{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "kots.io/v1beta1",
			Kind:       "Application",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "replicated-kots-app",
		},
		Spec: kotsv1beta1.ApplicationSpec{
			Title: "Replicated KOTS App",
			Icon:  "",
		},
	}
	return app
}

func releaseToFiles(release *Release) ([]types.UpstreamFile, error) {
	upstreamFiles := []types.UpstreamFile{}

	for filename, content := range release.Manifests {
		upstreamFile := types.UpstreamFile{
			Path:    filename,
			Content: content,
		}

		upstreamFiles = append(upstreamFiles, upstreamFile)
	}

	// Stash the user data for this search (we will readd at the end)
	userdataFiles := []types.UpstreamFile{}
	withoutUserdataFiles := []types.UpstreamFile{}
	for _, file := range upstreamFiles {
		d, _ := path.Split(file.Path)
		dirs := strings.Split(d, string(os.PathSeparator))

		if dirs[0] == "userdata" {
			userdataFiles = append(userdataFiles, file)
		} else {
			withoutUserdataFiles = append(withoutUserdataFiles, file)
		}
	}

	// remove any common prefix from all files
	if len(withoutUserdataFiles) > 0 {
		firstFileDir, _ := path.Split(withoutUserdataFiles[0].Path)
		commonPrefix := strings.Split(firstFileDir, string(os.PathSeparator))

		for _, file := range withoutUserdataFiles {
			d, _ := path.Split(file.Path)
			dirs := strings.Split(d, string(os.PathSeparator))

			commonPrefix = util.CommonSlicePrefix(commonPrefix, dirs)

		}

		cleanedUpstreamFiles := []types.UpstreamFile{}
		for _, file := range withoutUserdataFiles {
			d, f := path.Split(file.Path)
			d2 := strings.Split(d, string(os.PathSeparator))

			cleanedUpstreamFile := file
			d2 = d2[len(commonPrefix):]
			cleanedUpstreamFile.Path = path.Join(path.Join(d2...), f)

			cleanedUpstreamFiles = append(cleanedUpstreamFiles, cleanedUpstreamFile)
		}

		upstreamFiles = cleanedUpstreamFiles
	}

	upstreamFiles = append(upstreamFiles, userdataFiles...)

	return upstreamFiles, nil
}

// GetApplicationMetadata will return any available application yaml from
// the upstream. If there is no application.yaml, it will return
// a placeholder one
func GetApplicationMetadata(upstream *url.URL) ([]byte, error) {
	metadata, err := getApplicationMetadataFromHost("replicated.app", upstream)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get metadata from replicated.app")
	}

	if len(metadata) == 0 {
		metadata = []byte(DefaultMetadata)
	}

	return metadata, nil
}

func getApplicationMetadataFromHost(host string, upstream *url.URL) ([]byte, error) {
	r, err := parseReplicatedURL(upstream)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse replicated upstream")
	}

	url := fmt.Sprintf("https://%s/metadata/%s", host, r.AppSlug)

	if r.Channel != nil {
		url = fmt.Sprintf("%s/%s", url, *r.Channel)
	}

	getReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to call newrequest")
	}

	getReq.Header.Add("User-Agent", fmt.Sprintf("KOTS/%s", buildversion.Version()))

	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		return nil, errors.Wrap(err, "failed to execute get request")
	}
	defer getResp.Body.Close()

	if getResp.StatusCode == 404 {
		// no metadata is not an error
		return nil, nil
	}

	if getResp.StatusCode >= 400 {
		return nil, errors.Errorf("unexpected result from get request: %d", getResp.StatusCode)
	}

	respBody, err := ioutil.ReadAll(getResp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read response body")
	}

	return respBody, nil
}
