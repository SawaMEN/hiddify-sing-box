package awg

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/sagernet/sing-box/adapter"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	"github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/network"

	"github.com/amnezia-vpn/amneziawg-go/v3/conn"
	"github.com/amnezia-vpn/amneziawg-go/v3/device"
)

type DeviceOpts struct {
	UseIntegratedTun bool
	Address          []netip.Prefix
	AllowedIps       []netip.Prefix
	ExcludedIps      []netip.Prefix
	MTU              uint32
	Peer             netip.AddrPort   // H
	Fallback         []netip.AddrPort // H
	MTUDiscovery     bool             // H
	Reserved         *Reserved        // H
}

type Device struct {
	awgDevice *device.Device
	tun       tunAdapter
	bind      conn.Bind
	logger    *device.Logger
	ipcConfig string
	path      *devicePath // H
}

func NewDevice(ctx context.Context, logger logger.ContextLogger, dial network.Dialer, ipcConfig string, opts DeviceOpts) (*Device, error) {
	var (
		tun tunAdapter
		err error
	)

	if opts.UseIntegratedTun {
		tun, err = newSystemTun(ctx, opts.Address, opts.AllowedIps, opts.ExcludedIps, opts.MTU, logger)
		if err != nil {
			return nil, E.Cause(err, "create tunnel")
		}
	} else {
		tun, err = newNetworkTun(opts.Address, opts.MTU)
		if err != nil {
			return nil, err
		}
	}

	awgLogger := &device.Logger{
		Verbosef: func(format string, args ...any) {
			logger.Debug(fmt.Sprintf(strings.ToLower(format), args...))
		},
		Errorf: func(format string, args ...any) {
			logger.Error(fmt.Sprintf(strings.ToLower(format), args...))
		},
	}

	result := &Device{
		tun:       tun,
		bind:      newBind(ctx, dial, opts.Peer, opts.Reserved),
		logger:    awgLogger,
		ipcConfig: ipcConfig,
	}
	if opts.MTUDiscovery || len(opts.Fallback) > 0 {
		result.path = newDevicePath(ctx, logger, tun, opts)
	}
	return result, nil
}

func (d *Device) Start(stage adapter.StartStage) error {
	if stage != adapter.StartStateStart {
		return nil
	}

	d.awgDevice = device.NewDevice(d.tun, d.bind, d.logger)
	if err := d.awgDevice.IpcSet(d.ipcConfig); err != nil {
		return E.Cause(err, "set ipc config")
	}

	if err := d.tun.Start(); err != nil {
		return E.Cause(err, "tun start")
	}

	if err := d.awgDevice.Up(); err != nil {
		return err
	}
	if d.path != nil {
		d.path.start(d)
	}
	return nil
}

func (d *Device) IsUnderLoad() bool {
	if d.awgDevice == nil {
		return false
	}
	return d.awgDevice.IsUnderLoad()
}

func (d *Device) Close() error {
	if d.path != nil {
		d.path.close()
	}
	// Close the socket first to release writes that hold the device network lock. //H
	if d.bind != nil {
		_ = d.bind.Close()
	} // H
	if d.awgDevice != nil {
		d.awgDevice.Close()
	}
	return d.tun.Close()
}

func (d *Device) DialContext(ctx context.Context, network string, destination metadata.Socksaddr) (net.Conn, error) {
	return d.tun.DialContext(ctx, network, destination)
}

func (d *Device) ListenPacket(ctx context.Context, destination metadata.Socksaddr) (net.PacketConn, error) {
	return d.tun.ListenPacket(ctx, destination)
}

func (d *Device) IpcGet() (string, error) {
	if d.awgDevice == nil {
		return "", net.ErrClosed
	}
	return d.awgDevice.IpcGet()
}
