#!/usr/bin/env swift
//
// GPU Trace Generator - Create test traces for binary format analysis
//
// Generates traces with varying complexity:
// - Single encoder (1 dispatch)
// - Multiple encoders (2, 3, 4, 5, 6+ dispatches)
// - Known invocation counts
// - Known ALU characteristics
// - Varying occupancy patterns
//

import Metal
import Foundation
import os

extension JSONEncoder {
    static var pretty: JSONEncoder {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        return encoder
    }
}

// MARK: - Scenario Definitions

enum Scenario: String, CaseIterable {
    case singleEncoder = "01-single-encoder"
    case twoEncoders = "02-two-encoders"
    case threeEncoders = "03-three-encoders"
    case fourEncoders = "04-four-encoders"
    case sixEncoders = "06-six-encoders"
    case knownInvocations1000 = "known-invocations-1000"
    case knownInvocations10000 = "known-invocations-10000"
    case lowALU = "low-alu-simple-add"
    case highALU = "high-alu-complex-math"
    case lowOccupancy = "low-occupancy-high-registers"
    case highOccupancy = "high-occupancy-low-registers"
    case parityAsymmetric = "parity-asymmetric"

    var description: String {
        switch self {
        case .singleEncoder:
            return "Single encoder: 1 compute dispatch (1024 threads)"
        case .twoEncoders:
            return "Two encoders: 2 sequential dispatches"
        case .threeEncoders:
            return "Three encoders: 3 sequential dispatches"
        case .fourEncoders:
            return "Four encoders: 4 sequential dispatches"
        case .sixEncoders:
            return "Six encoders: 6 sequential dispatches (matches LLM trace)"
        case .knownInvocations1000:
            return "Known invocations: exactly 1000 (10 threadgroups × 100 threads)"
        case .knownInvocations10000:
            return "Known invocations: exactly 10000 (100 threadgroups × 100 threads)"
        case .lowALU:
            return "Low ALU utilization: simple add operation"
        case .highALU:
            return "High ALU utilization: complex mathematical operations"
        case .lowOccupancy:
            return "Low occupancy: high register pressure (large arrays)"
        case .highOccupancy:
            return "High occupancy: low register pressure (minimal state)"
        case .parityAsymmetric:
            return "Parity instrument: 4 command buffers, 3 labelled encoders, 1/3/7 dispatches"
        }
    }
}

// MARK: - Parity Ground Truth

struct ParityEncoderTruth: Codable {
    let label: String
    let kernel: String
    let dispatchCount: Int
}

final class ParityCommandBufferTruth: Codable {
    let label: String
    let encoder: ParityEncoderTruth?
    let cpuEncodeStartUptimeNS: UInt64
    var cpuEncodeEndUptimeNS: UInt64
    var cpuCommitUptimeNS: UInt64?
    var cpuCompleteUptimeNS: UInt64?
    var gpuStartTimeSeconds: Double?
    var gpuEndTimeSeconds: Double?
    var kernelStartTimeSeconds: Double?
    var kernelEndTimeSeconds: Double?

    init(label: String, encoder: ParityEncoderTruth?, cpuEncodeStartUptimeNS: UInt64) {
        self.label = label
        self.encoder = encoder
        self.cpuEncodeStartUptimeNS = cpuEncodeStartUptimeNS
        cpuEncodeEndUptimeNS = cpuEncodeStartUptimeNS
    }
}

struct ParityGroundTruth: Codable {
    let schemaVersion: Int
    let scenario: String
    let device: String
    let commandBuffers: [ParityCommandBufferTruth]
}

// MARK: - Metal Shaders

let shaderLibrary = """
#include <metal_stdlib>
using namespace metal;

// Simple add - minimal ALU, high occupancy
kernel void simple_add(
    device float* inputA [[buffer(0)]],
    device float* inputB [[buffer(1)]],
    device float* output [[buffer(2)]],
    uint id [[thread_position_in_grid]])
{
    output[id] = inputA[id] + inputB[id];
}

// Simple multiply
kernel void simple_multiply(
    device float* inputA [[buffer(0)]],
    device float* inputB [[buffer(1)]],
    device float* output [[buffer(2)]],
    uint id [[thread_position_in_grid]])
{
    output[id] = inputA[id] * inputB[id];
}

// Simple subtract
kernel void simple_subtract(
    device float* inputA [[buffer(0)]],
    device float* inputB [[buffer(1)]],
    device float* output [[buffer(2)]],
    uint id [[thread_position_in_grid]])
{
    output[id] = inputA[id] - inputB[id];
}

// Simple divide
kernel void simple_divide(
    device float* inputA [[buffer(0)]],
    device float* inputB [[buffer(1)]],
    device float* output [[buffer(2)]],
    uint id [[thread_position_in_grid]])
{
    output[id] = inputA[id] / (inputB[id] + 0.001); // Avoid div by zero
}

// Complex math - high ALU utilization
kernel void complex_math(
    device float* inputA [[buffer(0)]],
    device float* inputB [[buffer(1)]],
    device float* output [[buffer(2)]],
    uint id [[thread_position_in_grid]])
{
    float a = inputA[id];
    float b = inputB[id];

    // Many ALU operations
    float result = 0.0;
    for (int i = 0; i < 100; i++) {
        result += sin(a * i) * cos(b * i);
        result += exp(a * 0.01) * log(b + 1.0);
        result += pow(a, 1.5) * sqrt(b + 1.0);
        result += fmod(a * i, b + 1.0);
    }

    output[id] = result;
}

// Low occupancy - high register usage
kernel void high_register_pressure(
    device float* input [[buffer(0)]],
    device float* output [[buffer(1)]],
    uint id [[thread_position_in_grid]])
{
    // Allocate many registers
    float data[128];

    // Initialize
    for (int i = 0; i < 128; i++) {
        data[i] = input[id] * i;
    }

    // Complex computation using all registers
    float result = 0.0;
    for (int i = 0; i < 128; i++) {
        result += data[i] * data[(i + 1) % 128];
    }

    output[id] = result;
}

// High occupancy - minimal register usage
kernel void low_register_pressure(
    device float* input [[buffer(0)]],
    device float* output [[buffer(1)]],
    uint id [[thread_position_in_grid]])
{
    // Minimal registers
    output[id] = input[id] + 1.0;
}
"""

// MARK: - Trace Generator

class TraceGenerator {
    let device: MTLDevice
    let queue: MTLCommandQueue
    let library: MTLLibrary
    let captureManager: MTLCaptureManager
    let signpostLog = OSLog(subsystem: "com.tmc.gputrace.parity", category: "trace-generator")

    init?() {
        guard let device = MTLCreateSystemDefaultDevice() else {
            print("❌ Metal is not supported on this device")
            return nil
        }

        guard let queue = device.makeCommandQueue() else {
            print("❌ Failed to create command queue")
            return nil
        }

        guard let library = try? device.makeLibrary(source: shaderLibrary, options: nil) else {
            print("❌ Failed to compile shader library")
            return nil
        }

        self.device = device
        self.queue = queue
        self.library = library
        self.captureManager = MTLCaptureManager.shared()

        print("✓ Metal device: \(device.name)")
    }

    func startCapture(outputPath: String) throws {
        let descriptor = MTLCaptureDescriptor()
        descriptor.captureObject = device
        descriptor.destination = .gpuTraceDocument
        descriptor.outputURL = URL(fileURLWithPath: outputPath)

        // Remove existing trace
        try? FileManager.default.removeItem(atPath: outputPath)

        print("🎬 Starting GPU trace capture...")
        try captureManager.startCapture(with: descriptor)
    }

    func stopCapture() {
        if captureManager.isCapturing {
            captureManager.stopCapture()
            print("✅ Capture stopped")
        }
    }

    func run(scenario: Scenario, outputPath: String?, groundTruthPath: String?) {
        // Start capture if output path provided
        if let outputPath = outputPath {
            do {
                try startCapture(outputPath: outputPath)
            } catch {
                print("❌ Failed to start capture: \(error)")
                exit(1)
            }
        }
        print("\n" + String(repeating: "=", count: 60))
        print("Scenario: \(scenario.rawValue)")
        print("Description: \(scenario.description)")
        print(String(repeating: "=", count: 60))

        switch scenario {
        case .singleEncoder:
            runSingleEncoder()
        case .twoEncoders:
            runMultipleEncoders(count: 2)
        case .threeEncoders:
            runMultipleEncoders(count: 3)
        case .fourEncoders:
            runMultipleEncoders(count: 4)
        case .sixEncoders:
            runMultipleEncoders(count: 6)
        case .knownInvocations1000:
            runKnownInvocations(threadgroups: 10, threadsPerGroup: 100)
        case .knownInvocations10000:
            runKnownInvocations(threadgroups: 100, threadsPerGroup: 100)
        case .lowALU:
            runLowALU()
        case .highALU:
            runHighALU()
        case .lowOccupancy:
            runLowOccupancy()
        case .highOccupancy:
            runHighOccupancy()
        case .parityAsymmetric:
            runParityAsymmetric(groundTruthPath: groundTruthPath ?? defaultGroundTruthPath(outputPath: outputPath))
        }

        // Stop capture if it was started
        if outputPath != nil {
            stopCapture()

            if let path = outputPath, FileManager.default.fileExists(atPath: path) {
                let traceSize = (try? FileManager.default.attributesOfItem(atPath: path)[.size] as? Int64) ?? 0
                print()
                print("✅ GPU trace captured: \(path)")
                print("   Size: \(traceSize / 1024) KB")
            }
        }
    }

    // MARK: - Controlled Parity Instrument

    // runParityAsymmetric makes count-based joins fail loudly. Four command
    // buffers contain three compute encoders with 1, 3, and 7 dispatches; the
    // fourth command buffer is intentionally empty. Every capture-local label
    // includes a stable, content-bearing identifier.
    func runParityAsymmetric(groundTruthPath: String?) {
        let plans: [(commandBufferLabel: String, encoderLabel: String?, kernel: String?, dispatchCount: Int)] = [
            ("gputrace.parity.cb.alpha.1d", "gputrace.parity.encoder.alpha.simple_add.1d", "simple_add", 1),
            ("gputrace.parity.cb.bravo.3d", "gputrace.parity.encoder.bravo.simple_multiply.3d", "simple_multiply", 3),
            ("gputrace.parity.cb.charlie.7d", "gputrace.parity.encoder.charlie.simple_subtract.7d", "simple_subtract", 7),
            ("gputrace.parity.cb.delta.empty", nil, nil, 0),
        ]
        let bufferSize = 1024
        guard let (bufferA, bufferB, bufferC) = createBuffers(size: bufferSize) else { return }

        var records: [ParityCommandBufferTruth] = []
        var commandBuffers: [(MTLCommandBuffer, ParityCommandBufferTruth)] = []

        for plan in plans {
            let encodeSignpost = OSSignpostID(log: signpostLog)
            os_signpost(.begin, log: signpostLog, name: "Encode", signpostID: encodeSignpost, "%{public}s", plan.commandBufferLabel)
            let record = ParityCommandBufferTruth(
                label: plan.commandBufferLabel,
                encoder: plan.encoderLabel.flatMap { label in
                    plan.kernel.map { ParityEncoderTruth(label: label, kernel: $0, dispatchCount: plan.dispatchCount) }
                },
                cpuEncodeStartUptimeNS: DispatchTime.now().uptimeNanoseconds)
            guard let commandBuffer = queue.makeCommandBuffer() else {
                os_signpost(.end, log: signpostLog, name: "Encode", signpostID: encodeSignpost)
                print("❌ Failed to create command buffer for \(plan.commandBufferLabel)")
                return
            }
            commandBuffer.label = plan.commandBufferLabel

            if let encoderLabel = plan.encoderLabel, let kernel = plan.kernel {
                guard let pipeline = makePipeline(function: kernel),
                      let encoder = commandBuffer.makeComputeCommandEncoder() else {
                    os_signpost(.end, log: signpostLog, name: "Encode", signpostID: encodeSignpost)
                    print("❌ Failed to encode \(encoderLabel)")
                    return
                }
                encoder.label = encoderLabel
                encoder.setComputePipelineState(pipeline)
                encoder.setBuffer(bufferA, offset: 0, index: 0)
                encoder.setBuffer(bufferB, offset: 0, index: 1)
                encoder.setBuffer(bufferC, offset: 0, index: 2)
                let gridSize = MTLSize(width: bufferSize, height: 1, depth: 1)
                let threadGroupSize = MTLSize(width: 64, height: 1, depth: 1)
                for _ in 0..<plan.dispatchCount {
                    encoder.dispatchThreads(gridSize, threadsPerThreadgroup: threadGroupSize)
                }
                encoder.endEncoding()
            }
            record.cpuEncodeEndUptimeNS = DispatchTime.now().uptimeNanoseconds
            os_signpost(.end, log: signpostLog, name: "Encode", signpostID: encodeSignpost)

            let completionSignpost = OSSignpostID(log: signpostLog)
            os_signpost(.begin, log: signpostLog, name: "CommitToComplete", signpostID: completionSignpost, "%{public}s", plan.commandBufferLabel)
            commandBuffer.addCompletedHandler { completed in
                record.cpuCompleteUptimeNS = DispatchTime.now().uptimeNanoseconds
                record.gpuStartTimeSeconds = completed.gpuStartTime
                record.gpuEndTimeSeconds = completed.gpuEndTime
                record.kernelStartTimeSeconds = completed.kernelStartTime
                record.kernelEndTimeSeconds = completed.kernelEndTime
                os_signpost(.end, log: self.signpostLog, name: "CommitToComplete", signpostID: completionSignpost)
                os_signpost(.event, log: self.signpostLog, name: "Complete", "%{public}s", record.label)
            }
            record.cpuCommitUptimeNS = DispatchTime.now().uptimeNanoseconds
            commandBuffer.commit()
            records.append(record)
            commandBuffers.append((commandBuffer, record))
        }

        for (commandBuffer, _) in commandBuffers {
            commandBuffer.waitUntilCompleted()
        }

        guard let groundTruthPath else {
            print("⚠️ No ground-truth path supplied; run with --ground-truth to retain timing evidence")
            return
        }
        let truth = ParityGroundTruth(
            schemaVersion: 1,
            scenario: Scenario.parityAsymmetric.rawValue,
            device: device.name,
            commandBuffers: records)
        do {
            let data = try JSONEncoder.pretty.encode(truth)
            let url = URL(fileURLWithPath: groundTruthPath)
            try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            try data.write(to: url, options: .atomic)
            print("✓ Wrote parity ground truth: \(groundTruthPath)")
        } catch {
            print("❌ Write parity ground truth: \(error)")
        }
    }

    func defaultGroundTruthPath(outputPath: String?) -> String? {
        guard let outputPath else { return nil }
        let output = URL(fileURLWithPath: outputPath)
        return output.deletingPathExtension().appendingPathExtension("ground-truth.json").path
    }

    // MARK: - Single Encoder

    func runSingleEncoder() {
        let bufferSize = 1024
        guard let (bufferA, bufferB, bufferC) = createBuffers(size: bufferSize) else { return }
        guard let pipeline = makePipeline(function: "simple_add") else { return }

        guard let commandBuffer = queue.makeCommandBuffer() else { return }
        commandBuffer.label = "SingleEncoder"

        guard let encoder = commandBuffer.makeComputeCommandEncoder() else { return }
        encoder.label = "SimpleAdd"
        encoder.setComputePipelineState(pipeline)
        encoder.setBuffer(bufferA, offset: 0, index: 0)
        encoder.setBuffer(bufferB, offset: 0, index: 1)
        encoder.setBuffer(bufferC, offset: 0, index: 2)

        let gridSize = MTLSize(width: bufferSize, height: 1, depth: 1)
        let threadGroupSize = MTLSize(width: 64, height: 1, depth: 1)
        encoder.dispatchThreads(gridSize, threadsPerThreadgroup: threadGroupSize)
        encoder.endEncoding()

        commandBuffer.commit()
        commandBuffer.waitUntilCompleted()

        print("✓ Dispatched: \(bufferSize / 64) threadgroups × 64 threads = \(bufferSize) threads")
        print("  Expected encoders in trace: 1")
    }

    // MARK: - Multiple Encoders

    func runMultipleEncoders(count: Int) {
        let functions = ["simple_add", "simple_multiply", "simple_subtract", "simple_divide", "complex_math", "low_register_pressure"]
        let bufferSize = 1024

        guard let (bufferA, bufferB, bufferC) = createBuffers(size: bufferSize) else { return }
        guard let commandBuffer = queue.makeCommandBuffer() else { return }
        commandBuffer.label = "MultipleEncoders_\(count)"

        for i in 0..<count {
            let functionName = functions[i % functions.count]
            guard let pipeline = makePipeline(function: functionName) else { continue }

            guard let encoder = commandBuffer.makeComputeCommandEncoder() else { continue }
            encoder.label = "Encoder_\(i + 1)_\(functionName)"
            encoder.setComputePipelineState(pipeline)
            encoder.setBuffer(bufferA, offset: 0, index: 0)
            encoder.setBuffer(bufferB, offset: 0, index: 1)
            encoder.setBuffer(bufferC, offset: 0, index: 2)

            let gridSize = MTLSize(width: bufferSize, height: 1, depth: 1)
            let threadGroupSize = MTLSize(width: 64, height: 1, depth: 1)
            encoder.dispatchThreads(gridSize, threadsPerThreadgroup: threadGroupSize)
            encoder.endEncoding()

            print("  Encoder \(i + 1): \(functionName)")
        }

        commandBuffer.commit()
        commandBuffer.waitUntilCompleted()

        print("✓ Dispatched \(count) encoders")
        print("  Expected encoders in trace: \(count)")
    }

    // MARK: - Known Invocations

    func runKnownInvocations(threadgroups: Int, threadsPerGroup: Int) {
        let totalThreads = threadgroups * threadsPerGroup
        guard let (bufferA, bufferB, bufferC) = createBuffers(size: totalThreads) else { return }
        guard let pipeline = makePipeline(function: "simple_add") else { return }

        guard let commandBuffer = queue.makeCommandBuffer() else { return }
        commandBuffer.label = "KnownInvocations_\(totalThreads)"

        guard let encoder = commandBuffer.makeComputeCommandEncoder() else { return }
        encoder.label = "Invocations_\(totalThreads)"
        encoder.setComputePipelineState(pipeline)
        encoder.setBuffer(bufferA, offset: 0, index: 0)
        encoder.setBuffer(bufferB, offset: 0, index: 1)
        encoder.setBuffer(bufferC, offset: 0, index: 2)

        let gridSize = MTLSize(width: totalThreads, height: 1, depth: 1)
        let threadGroupSize = MTLSize(width: threadsPerGroup, height: 1, depth: 1)
        encoder.dispatchThreads(gridSize, threadsPerThreadgroup: threadGroupSize)
        encoder.endEncoding()

        commandBuffer.commit()
        commandBuffer.waitUntilCompleted()

        print("✓ Dispatched: \(threadgroups) threadgroups × \(threadsPerGroup) threads = \(totalThreads) threads")
        print("  Search for value \(totalThreads) in binary to find invocation count field")
    }

    // MARK: - ALU Variants

    func runLowALU() {
        runSingleEncoder() // Simple add = low ALU
        print("  Expected: Low ALU utilization (~5-10%)")
    }

    func runHighALU() {
        let bufferSize = 1024
        guard let (bufferA, bufferB, bufferC) = createBuffers(size: bufferSize) else { return }
        guard let pipeline = makePipeline(function: "complex_math") else { return }

        guard let commandBuffer = queue.makeCommandBuffer() else { return }
        commandBuffer.label = "HighALU"

        guard let encoder = commandBuffer.makeComputeCommandEncoder() else { return }
        encoder.label = "ComplexMath"
        encoder.setComputePipelineState(pipeline)
        encoder.setBuffer(bufferA, offset: 0, index: 0)
        encoder.setBuffer(bufferB, offset: 0, index: 1)
        encoder.setBuffer(bufferC, offset: 0, index: 2)

        let gridSize = MTLSize(width: bufferSize, height: 1, depth: 1)
        let threadGroupSize = MTLSize(width: 64, height: 1, depth: 1)
        encoder.dispatchThreads(gridSize, threadsPerThreadgroup: threadGroupSize)
        encoder.endEncoding()

        commandBuffer.commit()
        commandBuffer.waitUntilCompleted()

        print("✓ Complex math with many ALU operations")
        print("  Expected: High ALU utilization (>50%)")
    }

    // MARK: - Occupancy Variants

    func runLowOccupancy() {
        let bufferSize = 1024
        guard let (inputBuffer, outputBuffer) = createBuffers2(size: bufferSize) else { return }
        guard let pipeline = makePipeline(function: "high_register_pressure") else { return }

        guard let commandBuffer = queue.makeCommandBuffer() else { return }
        commandBuffer.label = "LowOccupancy"

        guard let encoder = commandBuffer.makeComputeCommandEncoder() else { return }
        encoder.label = "HighRegisterPressure"
        encoder.setComputePipelineState(pipeline)
        encoder.setBuffer(inputBuffer, offset: 0, index: 0)
        encoder.setBuffer(outputBuffer, offset: 0, index: 1)

        let gridSize = MTLSize(width: bufferSize, height: 1, depth: 1)
        let threadGroupSize = MTLSize(width: 32, height: 1, depth: 1) // Smaller groups due to register pressure
        encoder.dispatchThreads(gridSize, threadsPerThreadgroup: threadGroupSize)
        encoder.endEncoding()

        commandBuffer.commit()
        commandBuffer.waitUntilCompleted()

        print("✓ High register pressure (128 float array per thread)")
        print("  Expected: Low occupancy due to register usage")
    }

    func runHighOccupancy() {
        let bufferSize = 1024
        guard let (inputBuffer, outputBuffer) = createBuffers2(size: bufferSize) else { return }
        guard let pipeline = makePipeline(function: "low_register_pressure") else { return }

        guard let commandBuffer = queue.makeCommandBuffer() else { return }
        commandBuffer.label = "HighOccupancy"

        guard let encoder = commandBuffer.makeComputeCommandEncoder() else { return }
        encoder.label = "LowRegisterPressure"
        encoder.setComputePipelineState(pipeline)
        encoder.setBuffer(inputBuffer, offset: 0, index: 0)
        encoder.setBuffer(outputBuffer, offset: 0, index: 1)

        let gridSize = MTLSize(width: bufferSize, height: 1, depth: 1)
        let threadGroupSize = MTLSize(width: 256, height: 1, depth: 1) // Larger groups due to low register usage
        encoder.dispatchThreads(gridSize, threadsPerThreadgroup: threadGroupSize)
        encoder.endEncoding()

        commandBuffer.commit()
        commandBuffer.waitUntilCompleted()

        print("✓ Minimal register usage")
        print("  Expected: High occupancy")
    }

    // MARK: - Helper Functions

    func createBuffers(size: Int) -> (MTLBuffer, MTLBuffer, MTLBuffer)? {
        let bufferSize = size * MemoryLayout<Float>.stride

        guard let bufferA = device.makeBuffer(length: bufferSize, options: .storageModeShared),
              let bufferB = device.makeBuffer(length: bufferSize, options: .storageModeShared),
              let bufferC = device.makeBuffer(length: bufferSize, options: .storageModeShared) else {
            print("❌ Failed to create buffers")
            return nil
        }

        // Initialize data
        let dataA = bufferA.contents().bindMemory(to: Float.self, capacity: size)
        let dataB = bufferB.contents().bindMemory(to: Float.self, capacity: size)
        for i in 0..<size {
            dataA[i] = Float(i)
            dataB[i] = Float(i) * 2.0
        }

        return (bufferA, bufferB, bufferC)
    }

    func createBuffers2(size: Int) -> (MTLBuffer, MTLBuffer)? {
        let bufferSize = size * MemoryLayout<Float>.stride

        guard let inputBuffer = device.makeBuffer(length: bufferSize, options: .storageModeShared),
              let outputBuffer = device.makeBuffer(length: bufferSize, options: .storageModeShared) else {
            print("❌ Failed to create buffers")
            return nil
        }

        // Initialize data
        let data = inputBuffer.contents().bindMemory(to: Float.self, capacity: size)
        for i in 0..<size {
            data[i] = Float(i)
        }

        return (inputBuffer, outputBuffer)
    }

    func makePipeline(function: String) -> MTLComputePipelineState? {
        guard let function = library.makeFunction(name: function) else {
            print("❌ Failed to find function: \(function)")
            return nil
        }

        return try? device.makeComputePipelineState(function: function)
    }
}

// MARK: - Main

print("GPU Trace Generator")
print("==================")
print()

let args = CommandLine.arguments
func printUsage() {
    print("Usage: trace-generator <scenario> [output-path] [--ground-truth path]")
    print()
    print("Arguments:")
    print("  scenario     - Scenario to run (see below)")
    print("  output-path  - Optional .gputrace output path")
    print("  --ground-truth path  - Write controlled timing/label evidence to path")
    print()
    print("Available scenarios:")
    for scenario in Scenario.allCases {
        print("  \(scenario.rawValue)")
        print("    \(scenario.description)")
    }
    print()
    print("Examples:")
    print("  # With programmatic capture")
    print("  trace-generator parity-asymmetric output.gputrace --ground-truth truth.json")
    print()
    print("  # Or use the Makefile target:")
    print("  make run-capture SCENARIO=01-single-encoder")
}

if args.count < 2 {
    printUsage()
    exit(1)
}

if ["list", "--list", "-l"].contains(args[1]) {
    printUsage()
    exit(0)
}

guard let generator = TraceGenerator() else {
    exit(1)
}

let scenarioArg = args[1]
var outputPath: String?
var groundTruthPath: String?
var index = 2
while index < args.count {
    switch args[index] {
    case "--ground-truth":
        index += 1
        guard index < args.count else {
            print("❌ --ground-truth requires a path")
            exit(1)
        }
        groundTruthPath = args[index]
    default:
        guard outputPath == nil else {
            print("❌ Unexpected argument: \(args[index])")
            exit(1)
        }
        outputPath = args[index]
    }
    index += 1
}

if scenarioArg == "all" {
    if outputPath != nil {
        print("❌ Cannot use 'all' with output path")
        print("   Run scenarios individually with output paths")
        exit(1)
    }

    for scenario in Scenario.allCases {
        generator.run(scenario: scenario, outputPath: nil, groundTruthPath: nil)
        Thread.sleep(forTimeInterval: 0.5)
    }
} else {
    guard let scenario = Scenario(rawValue: scenarioArg) else {
        print("❌ Unknown scenario: \(scenarioArg)")
        print("Run without arguments to see available scenarios")
        exit(1)
    }

    generator.run(scenario: scenario, outputPath: outputPath, groundTruthPath: groundTruthPath)
}

if outputPath == nil {
    print()
    print("✓ Scenario completed (no trace captured)")
    print()
    print("To capture trace:")
    print("  make run-capture SCENARIO=\(scenarioArg)")
    print("  # or")
    print("  MTL_CAPTURE_ENABLED=1 trace-generator \(scenarioArg) output.gputrace")
}
