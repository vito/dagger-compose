package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"dagger.io/dagger"
	"github.com/compose-spec/compose-go/cli"
	"github.com/compose-spec/compose-go/types"
	"golang.org/x/sync/errgroup"
)

func main() {
	ctx := context.Background()

	opts, err := cli.NewProjectOptions(nil,
		cli.WithWorkingDirectory("."),
		cli.WithDefaultConfigPath)
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

	services := map[string]*dagger.Container{}
	for _, svc := range project.Services {
		ctr, err := buildService(c, project, svc)
		if err != nil {
			panic(err)
		}

		services[svc.Name] = ctr
	}

	for _, svc := range services {
		svc := svc
		eg.Go(func() error {
			return svc.Run(ctx)
		})
	}

	err = eg.Wait()
	if err != nil {
		panic(err)
	}
}

func buildService(c *dagger.Client, project *types.Project, svc types.ServiceConfig) (*dagger.Container, error) {
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

	for name, val := range svc.Environment {
		if val != nil {
			ctr = ctr.WithEnvVariable(name, *val)
		}
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

		svcCtr, err := buildService(c, project, cfg)
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
