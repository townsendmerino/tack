// FixtureCheck decodes what proto/fixtures/*.jsonl carry into the generated Swift types, re-encodes it and compares the
// JSON with the fixture's after normalization (DESIGN §9.3: the fixtures pass on both sides). It checks every
// session/event's event, and the params and result of each method listed in methodTypes. A stub of the app's Swift
// Testing suite until P4; CI builds and runs it on macOS:
//
//   xcrun swiftc -o /tmp/fixturecheck apps/macos/Sources/TackProto/*.swift apps/macos/Tests/FixtureCheck/main.swift
//   /tmp/fixturecheck proto/fixtures/*.jsonl
import Foundation

func normalized(_ data: Data) throws -> NSObject {
    try JSONSerialization.jsonObject(with: data, options: [.fragmentsAllowed]) as! NSObject
}

// roundTrip decodes JSON as T and encodes it back.
func roundTrip<T: Codable>(_: T.Type) -> (Data) throws -> Data {
    { try JSONEncoder().encode(try JSONDecoder().decode(T.self, from: $0)) }
}

// methodTypes are the methods whose params (in a client's request) and result (in the daemon's response to it) are
// checked, each through its generated types. A method added here is checked in every fixture that carries it.
let methodTypes: [String: (params: (Data) throws -> Data, result: (Data) throws -> Data)] = [
    Method.usageSummary: (roundTrip(UsageSummaryParams.self), roundTrip(UsageSummaryResult.self)),
]

var checked = 0
var methodsChecked = 0
var failures = 0

// check round-trips value through f and reports a difference or an error at where.
func check(_ value: Any, _ f: (Data) throws -> Data, _ where_: String) -> Bool {
    do {
        let raw = try JSONSerialization.data(withJSONObject: value, options: [.fragmentsAllowed])
        let back = try f(raw)
        if try normalized(raw) != normalized(back) {
            print("FAIL \(where_): re-encodes differently\n  fixture: \(String(data: raw, encoding: .utf8)!)\n  swift:   \(String(data: back, encoding: .utf8)!)")
            failures += 1
            return false
        }
        return true
    } catch {
        print("FAIL \(where_): \(error)")
        failures += 1
        return false
    }
}

for path in CommandLine.arguments.dropFirst() {
    let text = try String(contentsOfFile: path, encoding: .utf8)
    var pending: [String: String] = [:] // a checked request's id, as JSON text, to its method
    for (i, line) in text.split(separator: "\n").enumerated() where !line.isEmpty {
        let at = "\(path):\(i + 1)"
        let obj = try JSONSerialization.jsonObject(with: Data(line.utf8)) as! [String: Any]
        guard let msg = obj["msg"] as? [String: Any] else { continue }
        let id = msg["id"].map { String(data: try! JSONSerialization.data(withJSONObject: $0, options: [.fragmentsAllowed]), encoding: .utf8)! }
        if let method = msg["method"] as? String, let types = methodTypes[method] {
            if check(msg["params"] ?? [String: Any](), types.params, "\(at) \(method) params") {
                methodsChecked += 1
            }
            if let id = id {
                pending[id] = method
            }
            continue
        }
        if msg["method"] == nil, let id = id, let method = pending.removeValue(forKey: id), let result = msg["result"] {
            if check(result, methodTypes[method]!.result, "\(at) \(method) result") {
                methodsChecked += 1
            }
            continue
        }
        guard msg["method"] as? String == Method.sessionEvent,
              let params = msg["params"] as? [String: Any], let event = params["event"] else { continue }
        let raw = try JSONSerialization.data(withJSONObject: event)
        do {
            let decoded = try JSONDecoder().decode(Event.self, from: raw)
            if case .unknown = decoded.payload {
                print("FAIL \(at): event type \(decoded.type) is not in the generated EventPayload")
                failures += 1
                continue
            }
            let back = try JSONEncoder().encode(decoded)
            if try normalized(raw) != normalized(back) {
                print("FAIL \(at): re-encodes differently\n  fixture: \(String(data: raw, encoding: .utf8)!)\n  swift:   \(String(data: back, encoding: .utf8)!)")
                failures += 1
            }
            checked += 1
        } catch {
            print("FAIL \(at): \(error)")
            failures += 1
        }
    }
}
print("fixturecheck: \(checked) events and \(methodsChecked) method messages round-tripped, \(failures) failures")
if checked == 0 || methodsChecked == 0 || failures > 0 { exit(1) }
