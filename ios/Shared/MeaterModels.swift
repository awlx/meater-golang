import Foundation

// MARK: - Server models
//
// These mirror the JSON emitted by the Go server (internal/monitor/monitor.go
// and internal/store/store.go). Field names match the Go json tags exactly so
// plain Codable synthesis works.

/// Cooking states reported by the server.
enum CookState: String, Codable, Equatable {
    case idle
    case disconnected
    case waiting
    case cooking
    case stalled
    case ready

    // Tolerate states added server-side later instead of failing the decode.
    init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = CookState(rawValue: raw) ?? .waiting
    }
}

/// One status frame from /api/status or the /api/stream SSE feed.
struct ProbeStatus: Codable, Equatable {
    var connected: Bool
    var usingBridge: Bool
    var bridgeConnected: Bool
    var probeRssiDbm: Int
    var hasProbeRssi: Bool
    var tipCelsius: Double
    var tipFahrenheit: Double
    var ambientCelsius: Double
    var ambientFahrenheit: Double
    var targetCelsius: Double
    var targetFahrenheit: Double
    var rateCelsiusPerMin: Double
    var etaSeconds: Double      // -1 when unknown
    var etaSource: String       // "physics" | "history" | "blend"
    var etaLowSeconds: Double   // -1 when unknown
    var etaHighSeconds: Double  // -1 when unknown
    var etaSamples: Int
    var state: CookState
    var hasReading: Bool
    var running: Bool
    var cookName: String
    var meatType: String
    var cookId: Int64
    var startTipCelsius: Double
    var progressPercent: Double // 0-100; -1 when unknown
    var cookStartedAt: Date     // Go zero time when no cook is open
    var elapsedSeconds: Double  // -1 when no cook is open
    var updatedAt: Date

    /// Fraction of the way to target (0...1), matching the web UI's ring.
    var targetFraction: Double {
        guard hasReading, targetCelsius > 0 else { return 0 }
        return min(1, max(0, tipCelsius / targetCelsius))
    }

    /// Estimated finish wall-clock time, or nil while unknown.
    var etaDate: Date? {
        guard etaSeconds >= 0, hasReading else { return nil }
        return Date().addingTimeInterval(etaSeconds)
    }
}

/// One timestamped temperature sample from /api/history or /api/cooks/{id}.
struct HistoryPoint: Codable, Equatable {
    var at: Date
    var tipCelsius: Double
    var ambientCelsius: Double
}

/// Saved-cook metadata from /api/cooks.
struct CookMeta: Codable, Identifiable, Equatable {
    var id: Int64
    var name: String
    var meatType: String
    var startedAt: Date
    var endedAt: Date?
    var targetCelsius: Double
    var maxTipCelsius: Double
    var maxAmbientCelsius: Double
    var samples: Int
    var active: Bool

    var displayName: String {
        let trimmed = name.trimmingCharacters(in: .whitespaces)
        return trimmed.isEmpty ? "Cook #\(id)" : trimmed
    }
}

/// Response of GET /api/cooks/{id}.
struct CookDetail: Codable {
    var id: Int64
    var points: [HistoryPoint]
}

// MARK: - JSON coding for Go timestamps

/// Go's time.Time marshals as RFC3339 with up to nanosecond precision, which
/// ISO8601DateFormatter only partially accepts. Parse defensively.
enum GoJSON {
    static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .custom { decoder in
            let raw = try decoder.singleValueContainer().decode(String.self)
            if let date = parseDate(raw) { return date }
            throw DecodingError.dataCorrupted(.init(
                codingPath: decoder.codingPath,
                debugDescription: "unparseable RFC3339 date: \(raw)"))
        }
        return d
    }()

    static let encoder: JSONEncoder = {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        return e
    }()

    private static let isoFractional: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()

    private static let iso: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()

    static func parseDate(_ s: String) -> Date? {
        if let d = iso.date(from: s) { return d }
        if let d = isoFractional.date(from: s) { return d }
        // RFC3339Nano can carry more fractional digits than the ISO8601
        // formatter accepts; trim the fraction to milliseconds and retry.
        if let range = s.range(of: #"\.\d+"#, options: .regularExpression) {
            let trimmedFraction = String(s[range].prefix(4)) // "." + 3 digits
            let candidate = s.replacingCharacters(in: range, with: trimmedFraction)
            return isoFractional.date(from: candidate)
        }
        return nil
    }
}

extension Date {
    /// True for Go's zero time (year 1), which the server sends when no cook
    /// is open.
    var isGoZeroTime: Bool { timeIntervalSince1970 < -60_000_000_000 }
}
