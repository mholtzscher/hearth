package zigbee2mqtt

// Zigbee2MQTT expose access bits: 1 publishes state, 2 accepts set commands,
// and 4 supports get refresh.
const (
	exposePublishAccessBit = 1
	exposeSetAccessBit     = 2
	exposeGetAccessBit     = 4
)

func exposeCanPublish(expose upstreamExpose) bool {
	return expose.Access&exposePublishAccessBit != 0
}

func exposeCanSet(expose upstreamExpose) bool {
	return expose.Access&exposeSetAccessBit != 0
}

func exposeCanGet(expose upstreamExpose) bool {
	return expose.Access&exposeGetAccessBit != 0
}
