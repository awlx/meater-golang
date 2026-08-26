import SwiftUI
import WidgetKit

// MARK: - Timeline

struct CookEntry: TimelineEntry {
    var date: Date
    var snapshot: WidgetSnapshot?
}

struct CookStatusProvider: TimelineProvider {
    func placeholder(in context: Context) -> CookEntry {
        CookEntry(date: .now, snapshot: .preview)
    }

    func getSnapshot(in context: Context, completion: @escaping (CookEntry) -> Void) {
        if context.isPreview {
            completion(CookEntry(date: .now, snapshot: .preview))
            return
        }
        completion(CookEntry(date: .now, snapshot: SharedStore.loadSnapshot()))
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<CookEntry>) -> Void) {
        Task {
            // Prefer a fresh reading straight from the server; fall back to
            // the snapshot the app last wrote into the app group.
            let fresh = await fetchFresh()
            if let fresh { SharedStore.save(snapshot: fresh) }
            let snapshot = fresh ?? SharedStore.loadSnapshot()

            // While a cook is running ask for frequent redraws (WidgetKit
            // still budgets these); otherwise check back rarely.
            let running = snapshot?.running ?? false
            let next = Date().addingTimeInterval(running ? 5 * 60 : 60 * 60)
            completion(Timeline(entries: [CookEntry(date: .now, snapshot: snapshot)], policy: .after(next)))
        }
    }

    private func fetchFresh() async -> WidgetSnapshot? {
        guard let base = SharedStore.serverURL else { return nil }
        var req = URLRequest(url: base.appendingPathComponent("api/status"))
        req.timeoutInterval = 5
        guard let (data, response) = try? await URLSession.shared.data(for: req),
              (response as? HTTPURLResponse).map({ (200..<300).contains($0.statusCode) }) ?? true,
              let status = try? GoJSON.decoder.decode(ProbeStatus.self, from: data)
        else { return nil }
        return WidgetSnapshot(status: status, usesFahrenheit: SharedStore.usesFahrenheit)
    }
}

extension WidgetSnapshot {
    static var preview: WidgetSnapshot {
        let status = ProbeStatus(
            connected: true, usingBridge: false, bridgeConnected: false,
            probeRssiDbm: 0, hasProbeRssi: false,
            tipCelsius: 54.5, tipFahrenheit: 130.1,
            ambientCelsius: 121, ambientFahrenheit: 249.8,
            targetCelsius: 63, targetFahrenheit: 145.4,
            rateCelsiusPerMin: 0.4, etaSeconds: 1500, etaSource: "physics",
            etaLowSeconds: -1, etaHighSeconds: -1, etaSamples: 0,
            state: .cooking, hasReading: true, running: true,
            cookName: "Sunday roast", meatType: "beef", cookId: 1,
            startTipCelsius: 8, progressPercent: 84,
            cookStartedAt: Date().addingTimeInterval(-3600),
            elapsedSeconds: 3600, updatedAt: Date())
        return WidgetSnapshot(status: status, usesFahrenheit: false)
    }
}

// MARK: - Widget

struct CookStatusWidget: Widget {
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: "CookStatusWidget", provider: CookStatusProvider()) { entry in
            CookStatusWidgetView(entry: entry)
        }
        .configurationDisplayName("Cook status")
        .description("Internal temperature, target, and time remaining for the current cook.")
        .supportedFamilies([.systemSmall, .systemMedium, .accessoryCircular, .accessoryRectangular, .accessoryInline])
    }
}

struct CookStatusWidgetView: View {
    @Environment(\.widgetFamily) private var family
    var entry: CookEntry

    var body: some View {
        Group {
            switch family {
            case .accessoryCircular: CircularAccessoryView(snapshot: entry.snapshot)
            case .accessoryRectangular: RectangularAccessoryView(snapshot: entry.snapshot)
            case .accessoryInline: InlineAccessoryView(snapshot: entry.snapshot)
            case .systemMedium: MediumWidgetView(snapshot: entry.snapshot)
            default: SmallWidgetView(snapshot: entry.snapshot)
            }
        }
        .containerBackground(for: .widget) {
            if family == .systemSmall || family == .systemMedium {
                LinearGradient(colors: [Theme.card, Theme.bg], startPoint: .top, endPoint: .bottom)
            } else {
                Color.clear
            }
        }
    }
}

// MARK: - Shared bits

private struct MiniRing: View {
    let snapshot: WidgetSnapshot
    var lineWidth: CGFloat = 8

    var body: some View {
        ZStack {
            Circle().stroke(Color.white.opacity(0.08), lineWidth: lineWidth)
            Circle()
                .trim(from: 0, to: snapshot.progress)
                .stroke(
                    snapshot.state == .ready ? Theme.good : Theme.accent,
                    style: StrokeStyle(lineWidth: lineWidth, lineCap: .round))
                .rotationEffect(.degrees(-90))
        }
    }
}

private func stateLabel(_ snapshot: WidgetSnapshot?) -> String {
    guard let snapshot else { return "no data" }
    return snapshot.running ? snapshot.state.rawValue : "stopped"
}

// MARK: - Home screen families

struct SmallWidgetView: View {
    let snapshot: WidgetSnapshot?

    var body: some View {
        if let s = snapshot {
            VStack(spacing: 6) {
                ZStack {
                    MiniRing(snapshot: s)
                    VStack(spacing: 0) {
                        Text(Format.tempWhole(s.tipCelsius, fahrenheit: s.usesFahrenheit))
                            .font(.system(size: 26, weight: .heavy, design: .rounded))
                            .monospacedDigit()
                            .foregroundStyle(Theme.text)
                        Text("→ \(Format.tempWhole(s.targetCelsius, fahrenheit: s.usesFahrenheit))")
                            .font(.caption2.weight(.semibold))
                            .foregroundStyle(Theme.muted)
                    }
                }
                Text(stateLabel(s).capitalized)
                    .font(.caption2.weight(.bold))
                    .foregroundStyle(Theme.color(for: s.state))
            }
            .padding(2)
        } else {
            EmptyStateView()
        }
    }
}

struct MediumWidgetView: View {
    let snapshot: WidgetSnapshot?

    var body: some View {
        if let s = snapshot {
            HStack(spacing: 14) {
                ZStack {
                    MiniRing(snapshot: s, lineWidth: 9)
                    VStack(spacing: 0) {
                        Text(Format.tempWhole(s.tipCelsius, fahrenheit: s.usesFahrenheit))
                            .font(.system(size: 28, weight: .heavy, design: .rounded))
                            .monospacedDigit()
                            .foregroundStyle(Theme.text)
                        Text("of \(Format.tempWhole(s.targetCelsius, fahrenheit: s.usesFahrenheit))")
                            .font(.caption2.weight(.semibold))
                            .foregroundStyle(Theme.muted)
                    }
                }
                .frame(maxHeight: .infinity)

                VStack(alignment: .leading, spacing: 5) {
                    Text(s.cookName.isEmpty ? "ProbePilot" : s.cookName)
                        .font(.subheadline.weight(.bold))
                        .foregroundStyle(Theme.text)
                        .lineLimit(1)
                    Text(stateLabel(s).capitalized)
                        .font(.caption.weight(.bold))
                        .foregroundStyle(Theme.color(for: s.state))
                    HStack(spacing: 4) {
                        Image(systemName: "flame.fill")
                            .font(.caption2)
                            .foregroundStyle(Theme.cool)
                        Text("ambient \(Format.tempWhole(s.ambientCelsius, fahrenheit: s.usesFahrenheit))")
                            .font(.caption)
                            .foregroundStyle(Theme.muted)
                    }
                    if s.state == .ready {
                        Text("Ready to serve 🎉")
                            .font(.caption.weight(.bold))
                            .foregroundStyle(Theme.good)
                    } else if let eta = s.etaDate, eta > .now, s.running {
                        HStack(spacing: 4) {
                            Image(systemName: "timer")
                                .font(.caption2)
                                .foregroundStyle(Theme.accent2)
                            Text(timerInterval: Date.now...eta, countsDown: true)
                                .font(.caption.weight(.semibold))
                                .monospacedDigit()
                                .foregroundStyle(Theme.text)
                        }
                    }
                    Text("updated \(s.updatedAt.formatted(date: .omitted, time: .shortened))")
                        .font(.system(size: 9))
                        .foregroundStyle(Theme.muted.opacity(0.8))
                }
                Spacer(minLength: 0)
            }
        } else {
            EmptyStateView()
        }
    }
}

struct EmptyStateView: View {
    var body: some View {
        VStack(spacing: 4) {
            Text("🥩")
            Text("Open ProbePilot to connect")
                .font(.caption2)
                .foregroundStyle(Theme.muted)
                .multilineTextAlignment(.center)
        }
    }
}

// MARK: - Lock screen accessories

struct CircularAccessoryView: View {
    let snapshot: WidgetSnapshot?

    var body: some View {
        if let s = snapshot {
            Gauge(value: s.progress) {
                Text("°")
            } currentValueLabel: {
                Text(Format.tempWhole(s.tipCelsius, fahrenheit: s.usesFahrenheit))
                    .font(.system(.body, design: .rounded, weight: .bold))
                    .monospacedDigit()
            }
            .gaugeStyle(.accessoryCircular)
        } else {
            Gauge(value: 0) { Text("°") } currentValueLabel: { Text("--") }
                .gaugeStyle(.accessoryCircular)
        }
    }
}

struct RectangularAccessoryView: View {
    let snapshot: WidgetSnapshot?

    var body: some View {
        if let s = snapshot {
            VStack(alignment: .leading, spacing: 1) {
                Text(s.cookName.isEmpty ? "ProbePilot" : s.cookName)
                    .font(.headline)
                    .lineLimit(1)
                Text("\(Format.tempWhole(s.tipCelsius, fahrenheit: s.usesFahrenheit)) → \(Format.tempWhole(s.targetCelsius, fahrenheit: s.usesFahrenheit)) · \(stateLabel(s))")
                    .font(.caption)
                    .monospacedDigit()
                if s.state == .ready {
                    Text("Ready to serve")
                        .font(.caption.weight(.semibold))
                } else if let eta = s.etaDate, eta > .now, s.running {
                    Text(timerInterval: Date.now...eta, countsDown: true)
                        .font(.caption.weight(.semibold))
                        .monospacedDigit()
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        } else {
            Text("ProbePilot: no data")
                .font(.caption)
        }
    }
}

struct InlineAccessoryView: View {
    let snapshot: WidgetSnapshot?

    var body: some View {
        if let s = snapshot {
            Text("🥩 \(Format.tempWhole(s.tipCelsius, fahrenheit: s.usesFahrenheit)) → \(Format.tempWhole(s.targetCelsius, fahrenheit: s.usesFahrenheit))")
        } else {
            Text("🥩 ProbePilot: no data")
        }
    }
}

// MARK: - Previews

#Preview("Small", as: .systemSmall) {
    CookStatusWidget()
} timeline: {
    CookEntry(date: .now, snapshot: .preview)
}

#Preview("Medium", as: .systemMedium) {
    CookStatusWidget()
} timeline: {
    CookEntry(date: .now, snapshot: .preview)
}
