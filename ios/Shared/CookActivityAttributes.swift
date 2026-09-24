import ActivityKit
import Foundation

/// Live Activity payload for an in-progress cook. Compiled into both the app
/// (which starts/updates the activity) and the widget extension (which renders
/// it on the Lock Screen and in the Dynamic Island).
struct CookActivityAttributes: ActivityAttributes {
    struct ContentState: Codable, Hashable {
        var tipCelsius: Double
        var ambientCelsius: Double
        var targetCelsius: Double
        var progress: Double // 0...1
        var state: CookState
        var etaDate: Date?   // estimated finish; nil while unknown
        var usesFahrenheit: Bool
        var updatedAt: Date
    }

    /// Fixed for the lifetime of one cook.
    var cookName: String
    var meatType: String
    var startedAt: Date

    var displayName: String {
        let trimmed = cookName.trimmingCharacters(in: .whitespaces)
        return trimmed.isEmpty ? "Current cook" : trimmed
    }
}

extension CookActivityAttributes.ContentState {
    init(status: ProbeStatus, usesFahrenheit: Bool) {
        tipCelsius = status.tipCelsius
        ambientCelsius = status.ambientCelsius
        targetCelsius = status.targetCelsius
        progress = status.progressPercent >= 0
            ? min(1, max(0, status.progressPercent / 100))
            : status.targetFraction
        state = status.state
        etaDate = status.etaDate
        self.usesFahrenheit = usesFahrenheit
        updatedAt = status.updatedAt.isGoZeroTime ? Date() : status.updatedAt
    }
}
