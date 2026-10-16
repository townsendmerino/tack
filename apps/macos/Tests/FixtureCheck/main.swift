// FixtureCheck decodes every session/event in proto/fixtures/*.jsonl into the generated Swift types, re-encodes it and
// compares the JSON with the fixture's after normalization (DESIGN §9.3: the fixtures pass on both sides). A stub of
// the app's Swift Testing suite until P4; CI builds and runs it on macOS:
//
//   xcrun swiftc -o /tmp/fixturecheck apps/macos/Sources/TackProto/*.swift apps/macos/Tests/FixtureCheck/main.swift
//   /tmp/fixturecheck proto/fixtures/*.jsonl
import Foundation

func normalized(_ data: Data) throws -> NSObject {
    try JSONSerialization.jsonObject(with: data, options: [.fragmentsAllowed]) as! NSObject
}

var checked = 0
var failures = 0
for path in CommandLine.arguments.dropFirst() {
    let text = try String(contentsOfFile: path, encoding: .utf8)
    for (i, line) in text.split(separator: "\n").enumerated() where !line.isEmpty {
        let obj = try JSONSerialization.jsonObject(with: Data(line.utf8)) as! [String: Any]
        guard let msg = obj["msg"] as? [String: Any], msg["method"] as? String == Method.sessionEvent,
              let params = msg["params"] as? [String: Any], let event = params["event"] else { continue }
        let raw = try JSONSerialization.data(withJSONObject: event)
        do {
            let decoded = try JSONDecoder().decode(Event.self, from: raw)
            if case .unknown = decoded.payload {
                print("FAIL \(path):\(i + 1): event type \(decoded.type) is not in the generated EventPayload")
                failures += 1
                continue
            }
            let back = try JSONEncoder().encode(decoded)
            if try normalized(raw) != normalized(back) {
                print("FAIL \(path):\(i + 1): re-encodes differently\n  fixture: \(String(data: raw, encoding: .utf8)!)\n  swift:   \(String(data: back, encoding: .utf8)!)")
                failures += 1
            }
            checked += 1
        } catch {
            print("FAIL \(path):\(i + 1): \(error)")
            failures += 1
        }
    }
}
print("fixturecheck: \(checked) events round-tripped, \(failures) failures")
if checked == 0 || failures > 0 { exit(1) }
