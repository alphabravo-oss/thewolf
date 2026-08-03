package main

import (
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseadapter"
	"github.com/alphabravocompany/thewolf/internal/scannerreleasebackend"
)

func main() { scannerreleaseadapter.Main(scannerreleasebackend.AdapterLaneQuality) }
