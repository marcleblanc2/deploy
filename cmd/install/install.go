package main

import (
	"context"
	"os"
	"os/user"

	"github.com/sourcegraph/conc/iter"
	"github.com/sourcegraph/deploy/internal/containerd"
	"github.com/sourcegraph/deploy/internal/helm"
	"github.com/sourcegraph/deploy/internal/image"
	"github.com/sourcegraph/deploy/internal/k3s"
	"github.com/sourcegraph/deploy/internal/sourcegraph"
	"github.com/sourcegraph/deploy/internal/system/disk"
	"github.com/sourcegraph/deploy/internal/system/distro"
	"github.com/sourcegraph/deploy/internal/system/kernel"
	"github.com/sourcegraph/deploy/internal/system/service"
	"github.com/spf13/cobra"

	"github.com/sourcegraph/sourcegraph/lib/errors"
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "installs sourcegraph",
	RunE:  install,
}

func init() {
	rootCmd.AddCommand(installCmd)
}

func install(cmd *cobra.Command, args []string) error {
	// make sure we are running as root
	u, err := user.Current()
	if err != nil {
		return err
	}

	if u.Uid != "0" {
		return errors.Errorf("please rerun installer with root privileges")
	}

	// setup kernel parameters needed for Sourcegraph
	err = kernel.SetInotifyMaxUserWatches(cmd.Context(), 128_000)
	if err != nil {
		return err
	}

	err = kernel.SetVmMaxMapCount(cmd.Context(), 300_000)
	if err != nil {
		return err
	}

	err = kernel.SetSoftNProc(8_192)
	if err != nil {
		return err
	}

	err = kernel.SetHardNProc(16_384)
	if err != nil {
		return err
	}

	err = kernel.SetSoftNoFile(262_144)
	if err != nil {
		return err
	}

	err = kernel.SetHardNoFile(262_144)
	if err != nil {
		return err
	}

	if distro.IsAmazonLinux() {
		mounted, err := disk.IsMounted("/mnt/data", "/dev/nvme1n1")
		if err != nil {
			return err
		}

		if !mounted {
			err := disk.NewDisk(cmd.Context(), "/mnt/data", "/dev/nvme1n1", disk.XFS, disk.Mount())
			if err != nil {
				return err
			}
		}
	}

	err = k3s.LinkDataVolumes()
	if err != nil {
		return err
	}

	err = containerd.Install(cmd.Context())
	if err != nil {
		return err
	}

	iter.ForEach(image.Images(), func(img *string) {
		_ = image.Pull(cmd.Context(), *img)
	})

	err = k3s.Install(cmd.Context())
	if err != nil {
		return err
	}

	err = helm.Install()
	if err != nil {
		return err
	}

	err = sourcegraph.UnpackK8sConfigs()
	if err != nil {
		return err
	}

	if distro.IsAmazonLinux() {
		err = sourcegraph.WriteSourcegraphVersion(sgversion, "ec2-user")
		if err != nil {
			return err
		}
	}

	err = setupSGInit(cmd.Context())
	if err != nil {
		return err
	}

	err = setupSourcegraphd(cmd.Context())
	if err != nil {
		return err
	}

	return nil
}

func setupSGInit(ctx context.Context) error {
	srv, err := embeddedFS.ReadFile("bin/sg-init.service")
	if err != nil {
		return err
	}

	unpackedSrv, err := os.OpenFile("/etc/systemd/system/sg-init.service", os.O_RDWR|os.O_CREATE, 0754)
	if err != nil {
		return err
	}
	defer func() {
		_ = unpackedSrv.Close()
	}()

	_, err = unpackedSrv.Write(srv)
	if err != nil {
		return err
	}

	bin, err := embeddedFS.ReadFile("bin/sg-init")
	if err != nil {
		return err
	}

	unpackedBin, err := os.OpenFile("/usr/local/bin/sg-init", os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		return err
	}
	defer func() {
		_ = unpackedBin.Close()
	}()

	_, err = unpackedBin.Write(bin)
	if err != nil {
		return err
	}

	err = service.Enable(ctx, "sg-init.service")
	if err != nil {
		return err
	}

	return nil
}

func setupSourcegraphd(ctx context.Context) error {
	srv, err := embeddedFS.ReadFile("bin/sourcegraphd.service")
	if err != nil {
		return err
	}

	unpackedSrv, err := os.OpenFile("/etc/systemd/system/sourcegraphd.service", os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		return err
	}

	_, err = unpackedSrv.Write(srv)
	if err != nil {
		return err
	}

	bin, err := embeddedFS.ReadFile("bin/sourcegraphd")
	if err != nil {
		return err
	}

	unpackedBin, err := os.OpenFile("/usr/local/bin/sourcegraphd", os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		return err
	}
	defer func() {
		_ = unpackedBin.Close()
	}()

	_, err = unpackedBin.Write(bin)
	if err != nil {
		return err
	}

	err = service.Enable(ctx, "sourcegraphd.service")
	if err != nil {
		return err
	}

	return nil
}
