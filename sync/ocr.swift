// ocr: on-device handwriting recognition with Apple's Vision framework.
//   ocr <image.png>...   → JSON lines: {"file": ..., "text": ..., "confidence": ...}
import Foundation
import Vision

func recognize(_ path: String) -> (String, Float) {
    let url = URL(fileURLWithPath: path)
    let request = VNRecognizeTextRequest()
    request.recognitionLevel = .accurate
    request.usesLanguageCorrection = true
    request.recognitionLanguages = ["en-US"]
    let handler = VNImageRequestHandler(url: url, options: [:])
    do { try handler.perform([request]) } catch { return ("", 0) }
    let lines = (request.results ?? []).compactMap { $0.topCandidates(1).first }
    let text = lines.map(\.string).joined(separator: " ")
    let confidence = lines.isEmpty ? 0 : lines.map(\.confidence).reduce(0, +) / Float(lines.count)
    return (text, confidence)
}

for path in CommandLine.arguments.dropFirst() {
    let (text, confidence) = recognize(path)
    let line: [String: Any] = ["file": (path as NSString).lastPathComponent, "text": text, "confidence": confidence]
    let data = try! JSONSerialization.data(withJSONObject: line, options: [.sortedKeys])
    print(String(data: data, encoding: .utf8)!)
}
