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

	opts, err := cli.NewProjectOptions(os.Args[1:],
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
		daggerSvc, err := serviceContainer(c, project, svc)
		if err != nil {
			panic(err)
		}

		eg.Go(func() error {
			return daggerSvc.Run(ctx)
		})

		for _, port := range daggerSvc.PublishedPorts {
			port := port
			eg.Go(func() error {
				return daggerSvc.Socket(dagger.ContainerSocketOpts{
					Port:     port.Target,
					Protocol: port.Protocol,
				}).Bind(ctx, port.Address, dagger.SocketBindOpts{
					Family: port.Family,
				})
			})
		}
	}

	err = eg.Wait()
	if err != nil {
		panic(err)
	}
}

type Service struct {
	*dagger.Container

	PublishedPorts []PublishedPort
}

type PublishedPort struct {
	Address  string
	Family   dagger.NetworkFamily
	Target   int
	Protocol dagger.NetworkProtocol
}

func serviceContainer(c *dagger.Client, project *types.Project, svc types.ServiceConfig) (*Service, error) {
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

	published := []PublishedPort{}
	for _, port := range svc.Ports {
		switch port.Mode {
		case "ingress":
			publishedPort, err := strconv.Atoi(port.Published)
			if err != nil {
				return nil, err
			}

			ctr = ctr.WithExposedPort(int(port.Target))

			protocol := dagger.Tcp
			switch port.Protocol {
			case "udp":
				protocol = dagger.Udp
			case "", "tcp":
				protocol = dagger.Tcp
			default:
				return nil, fmt.Errorf("protocol %s not supported", port.Protocol)
			}

			published = append(published, PublishedPort{
				Address:  fmt.Sprintf(":%d", publishedPort),
				Family:   dagger.Ip,
				Target:   int(port.Target),
				Protocol: protocol,
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

		svc, err := serviceContainer(c, project, cfg)
		if err != nil {
			return nil, err
		}

		ctr = ctr.WithServiceBinding(depName, svc.Container)
	}

	var opts dagger.ContainerWithExecOpts
	if svc.Privileged {
		opts.InsecureRootCapabilities = true
	}

	ctr = ctr.WithExec(svc.Command, opts)

	return &Service{
		Container:      ctr,
		PublishedPorts: published,
	}, nil
}
