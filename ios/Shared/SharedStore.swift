import Foundation

/// Point-in-time cook state the app hands to the widget extension through the
/// shared app group container, so widgets render instantly even when they
/// can't reach the server.
struct WidgetSnapshot: Codable, Equatable {
    var tipCelsius: Double
    var ambientCelsius: Double
    var targetCelsius: Double
    var progress: Double // 0...1
    var state: CookState
    var cookName: String
    var meatType: String
    var etaDate: Date?
    var updatedAt: Date
    var usesFahrenheit: Bool
    var connected: Bool
    var running: Bool

    init(status: ProbeStatus, usesFahrenheit: Bool) {
        tipCelsius = status.tipCelsius
        ambientCelsius = status.ambientCelsius
        targetCelsius = status.targetCelsius
        progress = status.progressPercent >= 0
            ? min(1, max(0, status.progressPercent / 100))
            : status.targetFraction
        state = status.state
        cookName = status.cookName
        meatType = status.meatType
        etaDate = status.etaDate
        updatedAt = status.updatedAt.isGoZeroTime ? Date() : status.updatedAt
        self.usesFahrenheit = usesFahrenheit
        connected = status.connected
        running = status.running
    }
}

/// App-group-backed storage shared by the app and the widget extension.
///
/// If you change the app group id, also update ProbePilot.entitlements and
/// ProbePilotWidgets.entitlements to match.
enum SharedStore {
    static let appGroupID = "group.dev.awlx.probepilot"

    private static let snapshotKey = "widgetSnapshot"
    private static let serverURLKey = "serverURL"
    private static let fahrenheitKey = "usesFahrenheit"

    static var defaults: UserDefaults {
        UserDefaults(suiteName: appGroupID) ?? .standard
    }

    static func save(snapshot: WidgetSnapshot) {
        guard let data = try? GoJSON.encoder.encode(snapshot) else { return }
        defaults.set(data, forKey: snapshotKey)
    }

    static func loadSnapshot() -> WidgetSnapshot? {
        guard let data = defaults.data(forKey: snapshotKey) else { return nil }
        return try? GoJSON.decoder.decode(WidgetSnapshot.self, from: data)
    }

    /// Base URL of the Go server, e.g. http://meater.local:8080
    static var serverURLString: String {
        get { defaults.string(forKey: serverURLKey) ?? "" }
        set { defaults.set(newValue, forKey: serverURLKey) }
    }

    static var serverURL: URL? {
        var trimmed = serverURLString.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty else { return nil }
        // A bare host[:port] entry means a plain-HTTP LAN server.
        let lower = trimmed.lowercased()
        if !lower.hasPrefix("http://") && !lower.hasPrefix("https://") {
            trimmed = "http://" + trimmed
        }
        return URL(string: trimmed)
    }

    static var usesFahrenheit: Bool {
        get { defaults.bool(forKey: fahrenheitKey) }
        set { defaults.set(newValue, forKey: fahrenheitKey) }
    }
}
