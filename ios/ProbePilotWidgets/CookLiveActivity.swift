import ActivityKit
import SwiftUI
import WidgetKit

/// Live Activity for an in-progress cook: a rich Lock Screen card and full
/// Dynamic Island support (expanded, compact, minimal).
struct CookLiveActivity: Widget {
    var body: some WidgetConfiguration {
        ActivityConfiguration(for: CookActivityAttributes.self) { context in
            LockScreenCookView(context: context)
                .activityBackgroundTint(Theme.bg.opacity(0.86))
                .activitySystemActionForegroundColor(Theme.accent)
        } dynamicIsland: { context in
            DynamicIsland {
                DynamicIslandExpandedRegion(.leading) {
                    ExpandedLeadingView(state: context.state)
                }
                DynamicIslandExpandedRegion(.trailing) {
                    ExpandedTrailingView(state: context.state)
                }
                DynamicIslandExpandedRegion(.center) {
                    Text(context.attributes.displayName)
                        .font(.subheadline.weight(.bold))
                        .foregroundStyle(Theme.text)
                        .lineLimit(1)
                }
                DynamicIslandExpandedRegion(.bottom) {
                    ExpandedBottomView(context: context)
                }
            } compactLeading: {
                HStack(spacing: 3) {
                    Image(systemName: "thermometer.medium")
                        .foregroundStyle(Theme.accent)
                    Text(Format.tempWhole(context.state.tipCelsius, fahrenheit: context.state.usesFahrenheit))
                        .font(.caption.weight(.bold))
                        .monospacedDigit()
                        .foregroundStyle(Theme.text)
                }
            } compactTrailing: {
                CompactTrailingView(state: context.state)
            } minimal: {
                ProgressView(value: context.state.progress)
                    .progressViewStyle(.circular)
                    .tint(context.state.state == .ready ? Theme.good : Theme.accent)
            }
            .keylineTint(Theme.accent)
        }
    }
}

// MARK: - Lock Screen

private struct LockScreenCookView: View {
    let context: ActivityViewContext<CookActivityAttributes>

    private var state: CookActivityAttributes.ContentState { context.state }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                HStack(spacing: 6) {
                    Text("🥩")
                    Text(context.attributes.displayName)
                        .font(.subheadline.weight(.bold))
                        .foregroundStyle(Theme.text)
                        .lineLimit(1)
                }
                Spacer()
                Text(state.state.rawValue.capitalized)
                    .font(.caption.weight(.bold))
                    .foregroundStyle(Theme.color(for: state.state))
                    .padding(.horizontal, 10)
                    .padding(.vertical, 3)
                    .background(Capsule().fill(Theme.color(for: state.state).opacity(0.14)))
            }

            HStack(alignment: .firstTextBaseline) {
                HStack(alignment: .firstTextBaseline, spacing: 2) {
                    Text(Format.temp(state.tipCelsius, fahrenheit: state.usesFahrenheit))
                        .font(.system(size: 34, weight: .heavy, design: .rounded))
                        .monospacedDigit()
                        .foregroundStyle(Theme.text)
                    Text(Format.unitSuffix(fahrenheit: state.usesFahrenheit))
                        .font(.subheadline.weight(.bold))
                        .foregroundStyle(Theme.muted)
                }
                Text("of \(Format.tempWhole(state.targetCelsius, fahrenheit: state.usesFahrenheit))")
                    .font(.subheadline)
                    .foregroundStyle(Theme.muted)

                Spacer()

                if state.state == .ready {
                    Text("Ready 🎉")
                        .font(.headline.weight(.bold))
                        .foregroundStyle(Theme.good)
                } else if let eta = state.etaDate, eta > .now {
                    VStack(alignment: .trailing, spacing: 0) {
                        Text(timerInterval: Date.now...eta, countsDown: true)
                            .font(.title3.weight(.bold))
                            .monospacedDigit()
                            .multilineTextAlignment(.trailing)
                            .foregroundStyle(Theme.text)
                            .frame(maxWidth: 90)
                        Text("remaining")
                            .font(.caption2)
                            .foregroundStyle(Theme.muted)
                    }
                }
            }

            ProgressBar(progress: state.progress, ready: state.state == .ready)

            HStack {
                Label {
                    Text("ambient \(Format.tempWhole(state.ambientCelsius, fahrenheit: state.usesFahrenheit))")
                } icon: {
                    Image(systemName: "flame.fill")
                }
                .font(.caption)
                .foregroundStyle(Theme.cool)
                Spacer()
                Text("updated \(state.updatedAt.formatted(date: .omitted, time: .shortened))")
                    .font(.caption2)
                    .foregroundStyle(Theme.muted)
                    .opacity(context.isStale ? 1 : 0.7)
            }
        }
        .padding(14)
    }
}

private struct ProgressBar: View {
    let progress: Double
    let ready: Bool

    var body: some View {
        GeometryReader { geo in
            ZStack(alignment: .leading) {
                Capsule().fill(Color.white.opacity(0.08))
                Capsule()
                    .fill(ready
                        ? AnyShapeStyle(Theme.good)
                        : AnyShapeStyle(LinearGradient(
                            colors: [Theme.accent, Theme.accent2],
                            startPoint: .leading, endPoint: .trailing)))
                    .frame(width: max(8, geo.size.width * progress))
            }
        }
        .frame(height: 7)
    }
}

// MARK: - Dynamic Island pieces

private struct ExpandedLeadingView: View {
    let state: CookActivityAttributes.ContentState

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            HStack(alignment: .firstTextBaseline, spacing: 1) {
                Text(Format.tempWhole(state.tipCelsius, fahrenheit: state.usesFahrenheit))
                    .font(.title2.weight(.heavy))
                    .monospacedDigit()
                    .foregroundStyle(Theme.text)
            }
            Text("of \(Format.tempWhole(state.targetCelsius, fahrenheit: state.usesFahrenheit))")
                .font(.caption2)
                .foregroundStyle(Theme.muted)
        }
        .padding(.leading, 4)
    }
}

private struct ExpandedTrailingView: View {
    let state: CookActivityAttributes.ContentState

    var body: some View {
        VStack(alignment: .trailing, spacing: 1) {
            if state.state == .ready {
                Text("Ready 🎉")
                    .font(.headline.weight(.bold))
                    .foregroundStyle(Theme.good)
            } else if let eta = state.etaDate, eta > .now {
                Text(timerInterval: Date.now...eta, countsDown: true)
                    .font(.headline.weight(.bold))
                    .monospacedDigit()
                    .multilineTextAlignment(.trailing)
                    .foregroundStyle(Theme.text)
                    .frame(maxWidth: 68)
                Text("remaining")
                    .font(.caption2)
                    .foregroundStyle(Theme.muted)
            } else {
                Text(state.state.rawValue)
                    .font(.caption.weight(.bold))
                    .foregroundStyle(Theme.color(for: state.state))
            }
        }
        .padding(.trailing, 4)
    }
}

private struct ExpandedBottomView: View {
    let context: ActivityViewContext<CookActivityAttributes>

    var body: some View {
        VStack(spacing: 6) {
            ProgressBar(progress: context.state.progress, ready: context.state.state == .ready)
            HStack {
                Label {
                    Text("ambient \(Format.tempWhole(context.state.ambientCelsius, fahrenheit: context.state.usesFahrenheit))")
                } icon: {
                    Image(systemName: "flame.fill")
                }
                .font(.caption2)
                .foregroundStyle(Theme.cool)
                Spacer()
                Text(context.state.state.rawValue.capitalized)
                    .font(.caption2.weight(.bold))
                    .foregroundStyle(Theme.color(for: context.state.state))
            }
        }
        .padding(.top, 2)
    }
}

private struct CompactTrailingView: View {
    let state: CookActivityAttributes.ContentState

    var body: some View {
        if state.state == .ready {
            Image(systemName: "checkmark.circle.fill")
                .foregroundStyle(Theme.good)
        } else if let eta = state.etaDate, eta > .now {
            Text(timerInterval: Date.now...eta, countsDown: true, showsHours: false)
                .font(.caption.weight(.bold))
                .monospacedDigit()
                .frame(maxWidth: 44)
                .multilineTextAlignment(.trailing)
                .foregroundStyle(Theme.accent2)
        } else {
            ProgressView(value: state.progress)
                .progressViewStyle(.circular)
                .tint(Theme.accent)
        }
    }
}
