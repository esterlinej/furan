package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/dollarshaveclub/furan/pkg/client"
	"github.com/dollarshaveclub/furan/pkg/generated/furanrpc"
)

var monitorBuild bool
var buildArgs []string
var rpctimeout, buildtimeout time.Duration

// triggerCmd represents the trigger command
var triggerCmd = &cobra.Command{
	Use:   "trigger",
	Short: "Start a build on a remote Furan server",
	Long:  `Trigger and then monitor a build on a remote Furan server`,
	RunE:  trigger,
}

var triggerBuildRequest = furanrpc.BuildRequest{
	Build: &furanrpc.BuildDefinition{},
	Push:  &furanrpc.PushDefinition{},
}

var imagerepos, tags []string

var clientops client.Options

func init() {
	triggerCmd.PersistentFlags().StringVar(&clientops.Address, "remote-host", "", "Remote Furan server with gRPC port (eg: furan.me.com:4001)")
	triggerCmd.PersistentFlags().StringVar(&clientops.APIKey, "api-key", "", "API key")
	triggerCmd.PersistentFlags().BoolVar(&clientops.TLSInsecureSkipVerify, "tls-skip-verify", false, "Disable TLS certificate verification for RPC calls (INSECURE)")
	triggerCmd.PersistentFlags().StringVar(&triggerBuildRequest.Build.GithubCredential, "github-token", os.Getenv("GITHUB_TOKEN"), "github token")
	triggerCmd.PersistentFlags().StringVar(&triggerBuildRequest.Build.GithubRepo, "github-repo", "", "source github repo")
	triggerCmd.PersistentFlags().StringVar(&triggerBuildRequest.Build.Ref, "source-ref", "master", "source git ref")
	triggerCmd.PersistentFlags().StringVar(&triggerBuildRequest.Build.DockerfilePath, "dockerfile-path", "Dockerfile", "Dockerfile path (optional)")
	triggerCmd.PersistentFlags().StringArrayVar(&tags, "tags", []string{"master"}, "image tags (comma-delimited)")
	triggerCmd.PersistentFlags().BoolVar(&triggerBuildRequest.Build.TagWithCommitSha, "tag-sha", false, "additionally tag with git commit SHA (optional)")
	triggerCmd.PersistentFlags().BoolVar(&triggerBuildRequest.SkipIfExists, "skip-if-exists", false, "if build already exists at destination, skip build/push (registry: all tags exist, s3: object exists)")
	triggerCmd.PersistentFlags().BoolVar(&triggerBuildRequest.Build.DisableBuildCache, "disable-build-cache", false, "Disable build cache")
	triggerCmd.PersistentFlags().StringSliceVar(&buildArgs, "build-arg", []string{}, "Build arg to use for build request")
	triggerCmd.PersistentFlags().StringArrayVar(&imagerepos, "image-repos", []string{}, "Image repositories (comma-separated)")
	triggerCmd.PersistentFlags().BoolVar(&monitorBuild, "monitor", true, "Monitor build after triggering")
	triggerCmd.PersistentFlags().DurationVar(&rpctimeout, "timeout", 30*time.Second, "Timeout for RPC calls")
	triggerCmd.PersistentFlags().DurationVar(&buildtimeout, "build-timeout", 30*time.Minute, "Timeout for build duration")
	RootCmd.AddCommand(triggerCmd)
}

func trigger(cmd *cobra.Command, args []string) error {
	rb, err := client.New(clientops)
	if err != nil {
		return fmt.Errorf("error creating rpc client: %w", err)
	}
	defer rb.Close()

	triggerBuildRequest.Push.Registries = make([]*furanrpc.PushRegistryDefinition, len(imagerepos))
	for i := range imagerepos {
		triggerBuildRequest.Push.Registries[i] = &furanrpc.PushRegistryDefinition{
			Repo: imagerepos[i],
		}
	}

	triggerBuildRequest.Build.Tags = tags

	if !triggerBuildRequest.Build.TagWithCommitSha && len(tags) == 0 {
		return fmt.Errorf("at least one tag is required if not tagging with commit SHA")
	}

	ctx, cf := context.WithTimeout(context.Background(), rpctimeout)
	defer cf()

	id, err := rb.StartBuild(ctx, triggerBuildRequest)
	if err != nil {
		return fmt.Errorf("error triggering build: %w", err)
	}

	fmt.Fprintf(os.Stderr, "build started: %v\n", id)

	if monitorBuild {
		ctx2, cf2 := context.WithTimeout(context.Background(), buildtimeout)
		defer cf2()
		mc, err := rb.MonitorBuild(ctx2, id)
		if err != nil {
			return fmt.Errorf("error monitoring build: %w", err)
		}
		var prevstate furanrpc.BuildState
		var i uint
		for {
			be, err := mc.Recv()
			if err != nil {
				if err == io.EOF {
					fmt.Fprintf(os.Stderr, "build completed: state: %v (%d msgs received)\n", prevstate, i)
					return nil
				}
				return fmt.Errorf("error getting build message: %w", err)
			}
			fmt.Fprintf(os.Stderr, "%v (status: %v)\n", be.Message, be.CurrentState)
			prevstate = be.CurrentState
			i++
		}
	}

	return nil
}
