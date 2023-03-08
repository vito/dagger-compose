package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"

	"dagger.io/dagger"
	"github.com/compose-spec/compose-go/cli"
	"github.com/compose-spec/compose-go/types"
	"golang.org/x/sync/errgroup"
)

func main() {
	ctx := context.Background()

	opts, err := cli.NewProjectOptions(nil,
		cli.WithWorkingDirectory("."),
		cli.WithDefaultConfigPath,
		cli.WithOsEnv,
		cli.WithConfigFileEnv,
	)
	if err != nil {
		panic(err)
	}

	project, err := cli.ProjectFromOptions(opts)
	if err != nil {
		panic(err)
	}

	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer c.Close()

	eg, ctx := errgroup.WithContext(ctx)

	for _, svc := range project.Services {
		ctr, err := serviceContainer(c, project, svc)
		if err != nil {
			panic(err)
		}

		eg.Go(func() error {
			return ctr.Run(ctx)
		})
	}

	err = eg.Wait()
	if err != nil {
		panic(err)
	}
}

func serviceContainer(c *dagger.Client, project *types.Project, svc types.ServiceConfig) (*dagger.Container, error) {
	ctr := c.Pipeline(svc.Name).Container()
	if svc.Image != "" {
		ctr = ctr.From(svc.Image)
	} else if svc.Build != nil {
		args := []dagger.BuildArg{}
		for name, val := range svc.Build.Args {
			if val != nil {
				args = append(args, dagger.BuildArg{
					Name:  name,
					Value: *val,
				})
			}
		}

		ctr = ctr.Build(c.Host().Directory(svc.Build.Context), dagger.ContainerBuildOpts{
			Dockerfile: svc.Build.Dockerfile,
			BuildArgs:  args,
			Target:     svc.Build.Target,
		})
	}

	// sort env to ensure same container
	type env struct{ name, value string }
	envs := []env{}
	for name, val := range svc.Environment {
		if val != nil {
			envs = append(envs, env{name, *val})
		}
	}
	sort.Slice(envs, func(i, j int) bool {
		return envs[i].name < envs[j].name
	})
	for _, env := range envs {
		ctr = ctr.WithEnvVariable(env.name, env.value)
	}

	for _, port := range svc.Ports {
		switch port.Mode {
		case "ingress":
			publishedPort, err := strconv.Atoi(port.Published)
			if err != nil {
				return nil, err
			}

			ctr = ctr.WithExposedPort(int(port.Target), dagger.ContainerWithExposedPortOpts{
				Publish: publishedPort,
			})
		default:
			return nil, fmt.Errorf("port mode %s not supported", port.Mode)
		}
	}

	for _, expose := range svc.Expose {
		port, err := strconv.Atoi(expose)
		if err != nil {
			return nil, err
		}

		ctr = ctr.WithExposedPort(port)
	}

	for _, vol := range svc.Volumes {
		switch vol.Type {
		case types.VolumeTypeBind:
			ctr = ctr.WithMountedDirectory(vol.Target, c.Host().Directory(vol.Source))
		case types.VolumeTypeVolume:
			ctr = ctr.WithMountedCache(vol.Target, c.CacheVolume(vol.Source))
		default:
			return nil, fmt.Errorf("volume type %s not supported", vol.Type)
		}
	}

	for depName := range svc.DependsOn {
		cfg, err := project.GetService(depName)
		if err != nil {
			return nil, err
		}

		svcCtr, err := serviceContainer(c, project, cfg)
		if err != nil {
			return nil, err
		}

		ctr = ctr.WithServiceBinding(depName, svcCtr)
	}

	var opts dagger.ContainerWithExecOpts
	if svc.Privileged {
		opts.InsecureRootCapabilities = true
	}

	ctr = ctr.WithExec(svc.Command, opts)

	return ctr, nil
}
