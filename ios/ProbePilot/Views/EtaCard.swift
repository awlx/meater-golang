import SwiftUI

/// Estimated-time-remaining card with a locally ticking countdown between
/// server updates, plus the confidence note when history informs the figure.
struct EtaCard: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        // Tick once a second so the countdown runs smoothly between frames.
        TimelineView(.periodic(from: .now, by: 1)) { context in
            content(now: context.date)
        }
        .frame(maxWidth: .infinity)
        .card()
    }

    @ViewBuilder
    private func content(now: Date) -> some View {
        let status = model.status
        VStack(spacing: 5) {
            Text("ESTIMATED TIME REMAINING")
                .font(.caption.weight(.semibold))
                .kerning(1.5)
                .foregroundStyle(Theme.muted)

            if status?.state == .ready {
                Text("Done")
                    .font(.system(size: 46, weight: .heavy, design: .rounded))
                    .foregroundStyle(Theme.text)
                Text("Ready to serve 🎉")
                    .font(.subheadline.weight(.bold))
                    .foregroundStyle(Theme.good)
            } else if let status, status.hasReading, status.etaSeconds >= 0 {
                let sinceUpdate = status.updatedAt.isGoZeroTime ? 0 : now.timeIntervalSince(status.updatedAt)
                let remaining = max(0, status.etaSeconds - max(0, sinceUpdate))
                Text(Format.duration(remaining))
                    .font(.system(size: 46, weight: .heavy, design: .rounded))
                    .monospacedDigit()
                    .contentTransition(.numericText(countsDown: true))
                    .foregroundStyle(Theme.text)
                Text("≈ ready \(Format.clock(now.addingTimeInterval(remaining)))")
                    .font(.subheadline.weight(.semibold))
                    .monospacedDigit()
                    .foregroundStyle(Theme.text.opacity(0.9))
                if let note = historyNote(status: status, now: now) {
                    Text(note)
                        .font(.caption)
                        .foregroundStyle(Theme.muted)
                }
            } else {
                Text("--:--")
                    .font(.system(size: 46, weight: .heavy, design: .rounded))
                    .foregroundStyle(Theme.muted)
            }
        }
    }

    /// "based on 3 past cooks · 19:04–19:32" when the estimate uses history.
    private func historyNote(status: ProbeStatus, now: Date) -> String? {
        guard status.etaSource == "history" || status.etaSource == "blend",
              status.etaSamples > 0 else { return nil }
        var note = "based on \(status.etaSamples) past cook\(status.etaSamples == 1 ? "" : "s")"
        if status.etaLowSeconds >= 0, status.etaHighSeconds >= 0,
           status.etaHighSeconds - status.etaLowSeconds > 120 {
            let lo = now.addingTimeInterval(status.etaLowSeconds)
            let hi = now.addingTimeInterval(status.etaHighSeconds)
            note += " · \(Format.clock(lo))–\(Format.clock(hi))"
        }
        return note
    }
}
