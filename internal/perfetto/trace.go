// Package perfetto writes native Perfetto protobuf traces.
package perfetto

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"io"
	"sort"
	"strconv"
)

const (
	clockID    = 64 // Sequence-scoped user clock.
	sequenceID = 1
)

// EventKind describes how an event is represented in Perfetto.
type EventKind uint8

const (
	// EventSlice is a generic, nestable track slice.
	EventSlice EventKind = iota
	// EventInstant is a generic instant on a track.
	EventInstant
	// EventGPUCompute is a native GPU compute-stage event.
	EventGPUCompute
)

// Track identifies a Perfetto track. UUID must be non-zero and stable within
// an export.
type Track struct {
	UUID        uint64
	ParentUUID  uint64
	Name        string
	Description string
}

// Event is one source-backed item in a single clock domain. Times are
// nanoseconds in Trace.ClockDomain.
type Event struct {
	ID         uint64
	TrackUUID  uint64
	Name       string
	Category   string
	StartNS    uint64
	DurationNS uint64
	Kind       EventKind
	Args       map[string]any
}

// Counter is one source-backed GPU counter series.
type Counter struct {
	ID          uint32
	Name        string
	Description string
	Samples     []CounterSample
}

// CounterSample is one measured counter value.
type CounterSample struct {
	TimestampNS uint64
	Value       float64
}

// Trace is a deterministic projection of one measured clock domain.
type Trace struct {
	Identity    string
	ClockDomain string
	GPUName     string
	GPUModel    string
	Tracks      []Track
	Events      []Event
	Counters    []Counter
	Metadata    map[string]any
}

// WriteOptions controls optional lossy export. A zero MaxBytes writes every
// packet. MaxBytes counts logical, uncompressed protobuf bytes.
type WriteOptions struct {
	MaxBytes int64
}

// Receipt reports deterministic retention under an explicit output budget.
type Receipt struct {
	Policy            string
	LogicalBytes      int64
	EventsConsidered  int
	EventsRetained    int
	EventsDropped     int
	SamplesConsidered int
	SamplesRetained   int
	SamplesDropped    int
}

// TrackUUID returns a deterministic non-zero track UUID for a namespace and
// capture-local identity.
func TrackUUID(namespace, identity string) uint64 {
	h := fnv.New64a()
	_, _ = io.WriteString(h, namespace)
	_, _ = io.WriteString(h, "\x00")
	_, _ = io.WriteString(h, identity)
	id := h.Sum64()
	if id == 0 {
		return 1
	}
	return id
}

// Write writes trace as a binary perfetto.protos.Trace message.
func Write(w io.Writer, trace *Trace) error {
	_, err := WriteWithOptions(w, trace, WriteOptions{})
	return err
}

// WriteWithOptions writes trace and returns its retention receipt.
func WriteWithOptions(w io.Writer, trace *Trace, options WriteOptions) (Receipt, error) {
	if trace == nil {
		return Receipt{}, fmt.Errorf("write perfetto trace: nil trace")
	}
	if trace.ClockDomain == "" {
		return Receipt{}, fmt.Errorf("write perfetto trace: clock domain is required")
	}
	if err := validate(trace); err != nil {
		return Receipt{}, err
	}
	receipt := Receipt{Policy: "complete", EventsConsidered: len(trace.Events)}
	for _, counter := range trace.Counters {
		receipt.SamplesConsidered += len(counter.Samples)
	}

	var required [][]byte
	required = append(required, initialPacket(trace))

	tracks := append([]Track(nil), trace.Tracks...)
	sort.Slice(tracks, func(i, j int) bool { return tracks[i].UUID < tracks[j].UUID })
	for _, track := range tracks {
		required = append(required, trackDescriptorPacket(track))
	}
	root := TrackUUID("gputrace", trace.ClockDomain+":manifest")
	required = append(required, trackDescriptorPacket(Track{UUID: root, Name: "gputrace evidence manifest"}))
	if len(trace.Counters) > 0 {
		required = append(required, counterDescriptorPacket(trace.Counters))
	}

	groups := eventPacketGroups(trace.Identity, trace.Events, trace.Counters)
	selected := make([]packetGroup, 0, len(groups))
	if options.MaxBytes == 0 {
		selected = groups
	} else {
		const receiptReserve = int64(2048)
		used := framedSize(required)
		if used+receiptReserve > options.MaxBytes {
			return Receipt{}, fmt.Errorf("write perfetto trace: max output bytes %d cannot hold required descriptors and loss receipt", options.MaxBytes)
		}
		sort.SliceStable(groups, func(i, j int) bool { return groups[i].hash < groups[j].hash })
		for _, group := range groups {
			size := framedTimedSize(group.packets)
			if used+size+receiptReserve > options.MaxBytes {
				continue
			}
			selected = append(selected, group)
			used += size
		}
		receipt.Policy = "stable-identity-hash/v1"
	}
	for _, group := range selected {
		if group.class == "event" {
			receipt.EventsRetained++
		} else {
			receipt.SamplesRetained++
		}
	}
	receipt.EventsDropped = receipt.EventsConsidered - receipt.EventsRetained
	receipt.SamplesDropped = receipt.SamplesConsidered - receipt.SamplesRetained

	metadata := cloneMap(trace.Metadata)
	metadata["resource_policy"] = receipt.Policy
	metadata["logical_byte_boundary"] = options.MaxBytes
	metadata["events_considered"] = receipt.EventsConsidered
	metadata["events_retained"] = receipt.EventsRetained
	metadata["events_dropped"] = receipt.EventsDropped
	metadata["counter_samples_considered"] = receipt.SamplesConsidered
	metadata["counter_samples_retained"] = receipt.SamplesRetained
	metadata["counter_samples_dropped"] = receipt.SamplesDropped
	metadata["output_complete"] = receipt.EventsDropped == 0 && receipt.SamplesDropped == 0
	manifest := trackEventPacket(Event{TrackUUID: root, Name: "gputrace evidence manifest", Category: "gputrace", Kind: EventInstant, Args: metadata}, false)
	required = append(required, manifest)

	packets := flattenGroups(selected)
	var output bytes.Buffer
	writer := traceWriter{w: &output}
	for _, packet := range required {
		if err := writer.packet(packet); err != nil {
			return Receipt{}, err
		}
	}
	for _, packet := range packets {
		if err := writer.packet(packet.data); err != nil {
			return Receipt{}, err
		}
	}
	if options.MaxBytes > 0 && int64(output.Len()) > options.MaxBytes {
		return Receipt{}, fmt.Errorf("write perfetto trace: loss receipt exceeded reserved output budget")
	}
	receipt.LogicalBytes = int64(output.Len())
	if _, err := io.Copy(w, &output); err != nil {
		return Receipt{}, fmt.Errorf("write perfetto trace: %w", err)
	}
	return receipt, nil
}

func validate(trace *Trace) error {
	tracks := make(map[uint64]bool)
	for _, track := range trace.Tracks {
		if track.UUID == 0 {
			return fmt.Errorf("write perfetto trace: track UUID is zero")
		}
		if tracks[track.UUID] {
			return fmt.Errorf("write perfetto trace: duplicate track UUID %d", track.UUID)
		}
		tracks[track.UUID] = true
	}
	for _, event := range trace.Events {
		if event.Kind != EventGPUCompute && !tracks[event.TrackUUID] {
			return fmt.Errorf("write perfetto trace: event %q references unknown track %d", event.Name, event.TrackUUID)
		}
	}
	ids := make(map[uint32]bool)
	for _, counter := range trace.Counters {
		if counter.ID == 0 {
			return fmt.Errorf("write perfetto trace: counter %q has zero ID", counter.Name)
		}
		if ids[counter.ID] {
			return fmt.Errorf("write perfetto trace: duplicate counter ID %d", counter.ID)
		}
		ids[counter.ID] = true
	}
	return nil
}

type traceWriter struct{ w io.Writer }

func (w traceWriter) packet(packet []byte) error {
	var framed []byte
	framed = appendBytes(framed, 1, packet) // Trace.packet
	if _, err := w.w.Write(framed); err != nil {
		return fmt.Errorf("write perfetto trace: %w", err)
	}
	return nil
}

func initialPacket(trace *Trace) []byte {
	var traceClock []byte
	traceClock = appendUint(traceClock, 1, 11) // BUILTIN_CLOCK_TRACE_FILE
	traceClock = appendUint(traceClock, 2, 0)
	var clock []byte
	clock = appendUint(clock, 1, clockID)
	clock = appendUint(clock, 2, 0)
	var snapshot []byte
	snapshot = appendBytes(snapshot, 1, traceClock)
	snapshot = appendBytes(snapshot, 1, clock)
	snapshot = appendUint(snapshot, 2, 11) // primary trace clock

	var queue []byte
	queue = appendUint(queue, 1, 1)
	queue = appendString(queue, 2, "Apple GPU compute queue")
	queue = appendString(queue, 3, trace.ClockDomain+" clock")
	queue = appendUint(queue, 4, 0) // OTHER
	var stage []byte
	stage = appendUint(stage, 1, 2)
	stage = appendString(stage, 2, "Compute")
	stage = appendString(stage, 3, "Metal compute dispatch")
	stage = appendUint(stage, 4, 2) // COMPUTE
	var interned []byte
	interned = appendBytes(interned, 24, queue)
	interned = appendBytes(interned, 24, stage)

	var packet []byte
	packet = appendUint(packet, 10, sequenceID)
	packet = appendUint(packet, 13, 1) // SEQ_INCREMENTAL_STATE_CLEARED
	packet = appendBytes(packet, 6, snapshot)
	packet = appendBytes(packet, 12, interned)
	if trace.GPUName != "" || trace.GPUModel != "" {
		var gpu []byte
		gpu = appendString(gpu, 1, trace.GPUName)
		gpu = appendString(gpu, 2, "Apple")
		if trace.GPUModel != "" {
			gpu = appendString(gpu, 3, trace.GPUModel)
		}
		var info []byte
		info = appendBytes(info, 1, gpu)
		packet = appendBytes(packet, 128, info)
	}
	return packet
}

func packetHeader(timestamp uint64) []byte {
	var packet []byte
	packet = appendUint(packet, 8, timestamp)
	packet = appendUint(packet, 58, clockID)
	packet = appendUint(packet, 10, sequenceID)
	packet = appendUint(packet, 13, 2) // SEQ_NEEDS_INCREMENTAL_STATE
	return packet
}

func trackDescriptorPacket(track Track) []byte {
	var descriptor []byte
	descriptor = appendUint(descriptor, 1, track.UUID)
	descriptor = appendString(descriptor, 2, track.Name)
	if track.ParentUUID != 0 {
		descriptor = appendUint(descriptor, 5, track.ParentUUID)
	}
	if track.Description != "" {
		descriptor = appendString(descriptor, 14, track.Description)
	}
	packet := packetHeader(0)
	return appendBytes(packet, 60, descriptor)
}

type timedPacket struct {
	timestamp uint64
	order     uint8
	data      []byte
}

type packetGroup struct {
	class   string
	hash    uint64
	packets []timedPacket
}

func eventPacketGroups(identity string, events []Event, counters []Counter) []packetGroup {
	groups := make([]packetGroup, 0, len(events))
	for _, event := range events {
		group := packetGroup{class: "event", hash: identityHash(identity, "event", strconv.FormatUint(event.ID, 10), event.Name)}
		switch event.Kind {
		case EventGPUCompute:
			group.packets = append(group.packets, timedPacket{event.StartNS, 1, gpuEventPacket(event)})
		case EventInstant:
			group.packets = append(group.packets, timedPacket{event.StartNS, 1, trackEventPacket(event, false)})
		case EventSlice:
			group.packets = append(group.packets, timedPacket{event.StartNS, 1, trackEventPacket(event, false)})
			end := event
			end.StartNS += event.DurationNS
			group.packets = append(group.packets, timedPacket{end.StartNS, 0, trackEventPacket(end, true)})
		}
		groups = append(groups, group)
	}
	for _, counter := range counters {
		for index, sample := range counter.Samples {
			groups = append(groups, packetGroup{
				class:   "sample",
				hash:    identityHash(identity, "counter", strconv.FormatUint(uint64(counter.ID), 10), strconv.Itoa(index)),
				packets: []timedPacket{{sample.TimestampNS, 2, counterSamplePacket(counter.ID, sample)}},
			})
		}
	}
	return groups
}

func flattenGroups(groups []packetGroup) []timedPacket {
	var packets []timedPacket
	for _, group := range groups {
		packets = append(packets, group.packets...)
	}
	sort.SliceStable(packets, func(i, j int) bool {
		if packets[i].timestamp != packets[j].timestamp {
			return packets[i].timestamp < packets[j].timestamp
		}
		return packets[i].order < packets[j].order
	})
	return packets
}

func identityHash(parts ...string) uint64 {
	h := fnv.New64a()
	for _, part := range parts {
		_, _ = io.WriteString(h, part)
		_, _ = io.WriteString(h, "\x00")
	}
	return h.Sum64()
}

func framedSize(packets [][]byte) int64 {
	var size int64
	for _, packet := range packets {
		var framed []byte
		framed = appendBytes(framed, 1, packet)
		size += int64(len(framed))
	}
	return size
}

func framedTimedSize(packets []timedPacket) int64 {
	var size int64
	for _, packet := range packets {
		var framed []byte
		framed = appendBytes(framed, 1, packet.data)
		size += int64(len(framed))
	}
	return size
}

func cloneMap(values map[string]any) map[string]any {
	clone := make(map[string]any, len(values)+8)
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func gpuEventPacket(event Event) []byte {
	var gpu []byte
	gpu = appendUint(gpu, 1, event.ID)
	if event.DurationNS > 0 {
		gpu = appendUint(gpu, 2, event.DurationNS)
	}
	gpu = appendUint(gpu, 13, 1)
	gpu = appendUint(gpu, 14, 2)
	gpu = appendInt(gpu, 11, 0)
	gpu = appendString(gpu, 17, event.Name)
	for _, key := range sortedKeys(event.Args) {
		var extra []byte
		extra = appendString(extra, 1, key)
		extra = appendString(extra, 2, formatValue(event.Args[key]))
		gpu = appendBytes(gpu, 6, extra)
	}
	packet := packetHeader(event.StartNS)
	return appendBytes(packet, 53, gpu)
}

func trackEventPacket(event Event, end bool) []byte {
	var trackEvent []byte
	if end {
		trackEvent = appendUint(trackEvent, 9, 2) // TYPE_SLICE_END
	} else if event.Kind == EventInstant || event.DurationNS == 0 {
		trackEvent = appendUint(trackEvent, 9, 3) // TYPE_INSTANT
	} else {
		trackEvent = appendUint(trackEvent, 9, 1) // TYPE_SLICE_BEGIN
	}
	trackEvent = appendUint(trackEvent, 11, event.TrackUUID)
	if !end {
		trackEvent = appendString(trackEvent, 23, event.Name)
		if event.Category != "" {
			trackEvent = appendString(trackEvent, 22, event.Category)
		}
		for _, key := range sortedKeys(event.Args) {
			trackEvent = appendBytes(trackEvent, 4, debugAnnotation(key, event.Args[key]))
		}
	}
	packet := packetHeader(event.StartNS)
	return appendBytes(packet, 11, trackEvent)
}

func debugAnnotation(name string, value any) []byte {
	var annotation []byte
	annotation = appendString(annotation, 10, name)
	switch value := value.(type) {
	case bool:
		annotation = appendBool(annotation, 2, value)
	case int:
		annotation = appendInt(annotation, 4, int64(value))
	case int64:
		annotation = appendInt(annotation, 4, value)
	case uint64:
		annotation = appendUint(annotation, 3, value)
	case float64:
		annotation = appendDouble(annotation, 5, value)
	case string:
		annotation = appendString(annotation, 6, value)
	default:
		annotation = appendString(annotation, 6, formatValue(value))
	}
	return annotation
}

func counterDescriptorPacket(counters []Counter) []byte {
	counters = append([]Counter(nil), counters...)
	sort.Slice(counters, func(i, j int) bool { return counters[i].ID < counters[j].ID })
	var descriptor []byte
	for _, counter := range counters {
		var spec []byte
		spec = appendUint(spec, 1, uint64(counter.ID))
		spec = appendString(spec, 2, counter.Name)
		if counter.Description != "" {
			spec = appendString(spec, 3, counter.Description)
		}
		spec = appendUint(spec, 10, 6) // COMPUTE
		descriptor = appendBytes(descriptor, 1, spec)
	}
	var event []byte
	event = appendBytes(event, 1, descriptor)
	event = appendInt(event, 3, 0)
	packet := packetHeader(0)
	return appendBytes(packet, 52, event)
}

func counterSamplePacket(id uint32, sample CounterSample) []byte {
	var counter []byte
	counter = appendUint(counter, 1, uint64(id))
	counter = appendDouble(counter, 3, sample.Value)
	var event []byte
	event = appendBytes(event, 2, counter)
	event = appendInt(event, 3, 0)
	packet := packetHeader(sample.TimestampNS)
	return appendBytes(packet, 52, event)
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func formatValue(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	case bool:
		return strconv.FormatBool(value)
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case uint64:
		return strconv.FormatUint(value, 10)
	case float64:
		return strconv.FormatFloat(value, 'g', -1, 64)
	default:
		return fmt.Sprint(value)
	}
}
