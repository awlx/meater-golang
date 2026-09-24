import SwiftUI

/// The big progress ring: fraction of the way to target, glowing accent
/// stroke, temperature readout in the centre — a straight port of the web
/// UI's ring card.
struct RingView: View {
    let status: ProbeStatus?
    let usesFahrenheit: Bool

    private var fraction: Double { status?.targetFraction ?? 0 }
    private var isReady: Bool { status?.state == .ready }

    var body: some View {
        ZStack {
            Circle()
                .stroke(Color.white.opacity(0.06), style: StrokeStyle(lineWidth: 15))

            Circle()
                .trim(from: 0, to: fraction)
                .stroke(
                    isReady ? AnyShapeStyle(Theme.good) : AnyShapeStyle(Theme.ringGradient),
                    style: StrokeStyle(lineWidth: 15, lineCap: .round))
                .rotationEffect(.degrees(-90))
                .shadow(color: (isReady ? Theme.good : Theme.accent).opacity(0.45), radius: 6)
                .animation(.spring(duration: 0.6), value: fraction)
                .animation(.easeInOut(duration: 0.4), value: isReady)

            VStack(spacing: 4) {
                Text("INTERNAL")
                    .font(.caption2.weight(.semibold))
                    .kerning(2)
                    .foregroundStyle(Theme.muted)

                HStack(alignment: .firstTextBaseline, spacing: 1) {
                    Text(tipText)
                        .font(.system(size: 58, weight: .heavy, design: .rounded))
                        .monospacedDigit()
                        .contentTransition(.numericText())
                        .animation(.snappy, value: tipText)
                    Text("°")
                        .font(.system(size: 27, weight: .bold, design: .rounded))
                        .foregroundStyle(Theme.muted)
                }
                .foregroundStyle(Theme.text)
                .lineLimit(1)
                .minimumScaleFactor(0.5)

                Text(targetText)
                    .font(.footnote)
                    .foregroundStyle(Theme.muted)
            }
            .padding(30)
        }
        .frame(maxWidth: 300)
        .aspectRatio(1, contentMode: .fit)
    }

    private var tipText: String {
        guard let status, status.hasReading else { return "--" }
        return Format.temp(status.tipCelsius, fahrenheit: usesFahrenheit)
    }

    private var targetText: String {
        guard let status, status.targetCelsius > 0 else { return "target --°" }
        return "target \(Format.temp(status.targetCelsius, fahrenheit: usesFahrenheit))\(Format.unitSuffix(fahrenheit: usesFahrenheit))"
    }
}
