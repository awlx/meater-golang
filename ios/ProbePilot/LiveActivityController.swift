import ActivityKit
import Foundation

/// Owns the cook Live Activity: starts one when a cook with readings is
/// running, pushes state updates as SSE frames arrive, and ends it when the
/// session stops.
@MainActor
final class LiveActivityController {
    private var activity: Activity<CookActivityAttributes>?
    private var lastPushedState: CookActivityAttributes.ContentState?
    private var lastPushAt: Date = .distantPast

    /// Between pushes, small temperature jitter isn't worth waking the Lock
    /// Screen for; push at most every few seconds unless something visible
    /// changed.
    private let minPushInterval: TimeInterval = 5

    init() {
        // Re-adopt an activity left over from a previous app run so we update
        // it instead of leaking a stale one.
        activity = Activity<CookActivityAttributes>.activities.first
    }

    var isActive: Bool { activity != nil }

    func sync(status: ProbeStatus, usesFahrenheit: Bool, enabled: Bool) {
        guard enabled else {
            endIfNeeded()
            return
        }
        guard status.running, status.hasReading else {
            // Idle or stopped: a finished cook keeps its "ready" activity
            // until the session stops, matching the web UI's lifecycle.
            if !status.running { endIfNeeded() }
            return
        }

        let state = CookActivityAttributes.ContentState(status: status, usesFahrenheit: usesFahrenheit)
        if activity == nil {
            start(status: status, state: state)
        } else {
            update(state: state)
        }
    }

    func endIfNeeded() {
        guard let activity else { return }
        self.activity = nil
        lastPushedState = nil
        let finalState = activity.content.state
        Task {
            await activity.end(
                ActivityContent(state: finalState, staleDate: nil),
                dismissalPolicy: .after(.now + 15 * 60))
        }
    }

    private func start(status: ProbeStatus, state: CookActivityAttributes.ContentState) {
        guard ActivityAuthorizationInfo().areActivitiesEnabled else { return }
        let attributes = CookActivityAttributes(
            cookName: status.cookName,
            meatType: status.meatType,
            startedAt: status.cookStartedAt.isGoZeroTime ? Date() : status.cookStartedAt)
        do {
            activity = try Activity.request(
                attributes: attributes,
                content: ActivityContent(state: state, staleDate: staleDate()))
            lastPushedState = state
            lastPushAt = Date()
        } catch {
            // Live Activities can be disabled per-app; nothing to do.
        }
    }

    private func update(state: CookActivityAttributes.ContentState) {
        guard let activity else { return }
        let significant = lastPushedState.map { isSignificantChange(from: $0, to: state) } ?? true
        guard significant || Date().timeIntervalSince(lastPushAt) >= minPushInterval else { return }
        lastPushedState = state
        lastPushAt = Date()
        let content = ActivityContent(state: state, staleDate: staleDate())
        Task { await activity.update(content) }
    }

    private func isSignificantChange(
        from old: CookActivityAttributes.ContentState,
        to new: CookActivityAttributes.ContentState
    ) -> Bool {
        old.state != new.state
            || abs(old.tipCelsius - new.tipCelsius) >= 0.5
            || abs(old.targetCelsius - new.targetCelsius) >= 0.5
            || old.usesFahrenheit != new.usesFahrenheit
    }

    /// Without a push channel the activity only updates while the app runs;
    /// mark data stale if nothing has arrived for a while so the UI can dim.
    private func staleDate() -> Date { .now + 15 * 60 }
}
