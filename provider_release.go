package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver"
	"github.com/google/go-github/v32/github"
	"github.com/pkg/errors"
)

type providerRelease struct {
	version *semver.Version
	plugins map[string]string
}

func (p providerRelease) generateShims(dir, pluginName string) error {
	for arch, url := range p.plugins {

		pluginDir := filepath.Join(dir, "plugins", arch)

		log.Println("[DEBUG] plugin dir", pluginDir)

		st, err := os.Stat(pluginDir)
		if err != nil && !os.IsNotExist(err) {
			return errors.Wrapf(err, "while getting stat of %s", pluginDir)
		}

		if err == nil && !st.IsDir() {
			return errors.Errorf("%s is not a directory", pluginDir)
		}

		if os.IsNotExist(err) {
			log.Println("[DEBUG] creating plugin dir", pluginDir)
			err = os.MkdirAll(pluginDir, 0700)
			if err != nil {
				return errors.Wrapf(err, "while creating dir %s", pluginDir)
			}
		}

		binaryName := fmt.Sprintf("terraform-provider-%s_v%s", pluginName, p.version.String())
		log.Println("[DEBUG] binary name", binaryName)

		shimText, err := generateShim(url, pluginName, p.version.String(), binaryName)

		shimFileName := filepath.Join(pluginDir, binaryName)

		log.Println("[DEBUG] shim file name", shimFileName)

		err = ioutil.WriteFile(shimFileName, []byte(shimText), 0700)
		if err != nil {
			return errors.Wrapf(err, "while writing to file %s", shimFileName)
		}

	}

	return nil

}

func repoReleaseToProviderRelease(r *github.RepositoryRelease) (providerRelease, error) {
	ver, err := semver.NewVersion(r.GetTagName())
	if err != nil {
		return providerRelease{}, nil
	}

	pr := providerRelease{
		version: ver,
		plugins: make(map[string]string),
	}

	for _, a := range r.Assets {
		matches, arch := matchTerraformProviderAssetName(a.GetName())

		// there is no point in generating windows shim, so skip it.
		if matches && !strings.Contains(arch, "windows") {
			pr.plugins[arch] = a.GetBrowserDownloadURL()
		}
	}

	return pr, nil

}

func findLatestReleaseWithPluginAssets(ctx context.Context, gcl *github.Client, owner, repo string, constraints *semver.Constraints) (providerRelease, error) {

	listOptions := &github.ListOptions{
		PerPage: 50,
	}

	providerReleases := []providerRelease{}

	for {
		releases, response, err := gcl.Repositories.ListReleases(ctx, owner, repo, listOptions)
		if err != nil {
			return providerRelease{}, errors.Wrap(err, "while listing releases")
		}

		for _, r := range releases {
			pr, err := repoReleaseToProviderRelease(r)
			if err == nil {
				if constraints.Check(pr.version) {
					providerReleases = append(providerReleases, pr)
				}
			}
		}

		if response.NextPage == 0 {
			break
		}

		listOptions.Page = response.NextPage
	}

	if len(providerReleases) == 0 {
		return providerRelease{}, errors.New("could not find any releases containing terraform provider artifacts")
	}

	sort.Slice(providerReleases, func(i, j int) bool {
		return providerReleases[i].version.Compare(providerReleases[j].version) > 0
	})

	return providerReleases[0], nil

}
