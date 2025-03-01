package main

import (
	"flag"
	"fmt"
	"os"
	"path"

	"k8s.io/klog/v2"

	"github.com/MooreThreads/csi-driver-3fs/pkg/driver"
)

var (
	conf        driver.Config
	showVersion bool
)

func init() {
	flag.StringVar(&conf.VendorVersion, "version", "v0.1.0", "CSI vendor version")
	flag.StringVar(&conf.Endpoint, "endpoint", "unix://tmp/csi.sock", "CSI endpoint")
	flag.StringVar(&conf.DriverName, "drivername", "3fs.csi.mthreads.com", "name of the driver")
	flag.StringVar(&conf.StateDir, "statedir", "/3fs/stage", "directory for storing state and volumes")
	flag.StringVar(&conf.NodeID, "nodeid", "", "node id")
	flag.BoolVar(&conf.EnableAttach, "enable-attach", true, "Enables RPC_PUBLISH_UNPUBLISH_VOLUME capability.")
	flag.Int64Var(&conf.MaxVolumeSize, "max-volume-size", 1024*1024*1024*1024, "maximum size of volumes in bytes")
	flag.BoolVar(&showVersion, "showVersion", false, "Show version.")
}

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	if showVersion {
		baseName := path.Base(os.Args[0])
		fmt.Println(baseName, conf.VendorVersion)
		return
	}

	if conf.MaxVolumeExpansionSizeNode == 0 {
		conf.MaxVolumeExpansionSizeNode = conf.MaxVolumeSize
	}

	driver, err := driver.NewCsi3fsDriver(conf)
	if err != nil {
		fmt.Printf("Failed to initialize driver: %s", err.Error())
		os.Exit(1)
	}

	if err := driver.Run(); err != nil {
		fmt.Printf("Failed to run driver: %s", err.Error())
		os.Exit(1)
	}
}
