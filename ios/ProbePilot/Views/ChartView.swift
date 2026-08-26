import Charts
import SwiftUI

/// Temperature-over-time chart: internal + ambient lines, dashed target rule,
/// and a scrub-to-inspect overlay — the app's version of the web canvas chart.
struct ChartView: View {
    let points: [ChartPoint]
    let targetCelsius: Double?
    let usesFahrenheit: Bool
    var emptyMessage = "Collecting data…"

    @State private var selectedTime: Date?

    private func display(_ celsius: Double) -> Double {
        Format.celsius(toDisplay: celsius, fahrenheit: usesFahrenheit)
    }

    var body: some View {
        if points.count < 2 {
            Text(emptyMessage)
                .font(.footnote)
                .foregroundStyle(Theme.muted)
                .frame(maxWidth: .infinity, minHeight: 220)
        } else {
            chart
        }
    }

    private var selectedPoint: ChartPoint? {
        guard let selectedTime else { return nil }
        return points.min {
            abs($0.time.timeIntervalSince(selectedTime)) < abs($1.time.timeIntervalSince(selectedTime))
        }
    }

    private var chart: some View {
        Chart {
            ForEach(points) { p in
                LineMark(
                    x: .value("Time", p.time),
                    y: .value("Temp", display(p.ambient)),
                    series: .value("Series", "Ambient"))
                    .foregroundStyle(Theme.cool)
                    .lineStyle(StrokeStyle(lineWidth: 2, lineJoin: .round))
                    .interpolationMethod(.monotone)
            }
            ForEach(points) { p in
                LineMark(
                    x: .value("Time", p.time),
                    y: .value("Temp", display(p.tip)),
                    series: .value("Series", "Internal"))
                    .foregroundStyle(Theme.accent)
                    .lineStyle(StrokeStyle(lineWidth: 2.5, lineJoin: .round))
                    .interpolationMethod(.monotone)
            }
            if let targetCelsius, targetCelsius > 0 {
                RuleMark(y: .value("Target", display(targetCelsius)))
                    .foregroundStyle(Theme.good.opacity(0.6))
                    .lineStyle(StrokeStyle(lineWidth: 1.5, dash: [5, 4]))
            }
            if let sel = selectedPoint {
                RuleMark(x: .value("Selected", sel.time))
                    .foregroundStyle(Color.white.opacity(0.25))
                    .lineStyle(StrokeStyle(lineWidth: 1))
                PointMark(x: .value("Time", sel.time), y: .value("Temp", display(sel.tip)))
                    .foregroundStyle(Theme.accent)
                PointMark(x: .value("Time", sel.time), y: .value("Temp", display(sel.ambient)))
                    .foregroundStyle(Theme.cool)
            }
        }
        .chartYScale(domain: yDomain)
        .chartXSelection(value: $selectedTime)
        .chartXAxis {
            AxisMarks(values: .automatic(desiredCount: 4)) { _ in
                AxisGridLine().foregroundStyle(Color.white.opacity(0.06))
                AxisValueLabel(format: .dateTime.hour().minute())
                    .foregroundStyle(Theme.muted)
            }
        }
        .chartYAxis {
            AxisMarks(position: .leading, values: .automatic(desiredCount: 5)) { value in
                AxisGridLine().foregroundStyle(Color.white.opacity(0.06))
                if let temp = value.as(Double.self) {
                    AxisValueLabel("\(Int(temp))°").foregroundStyle(Theme.muted)
                }
            }
        }
        .frame(height: 220)
        .overlay(alignment: .topLeading) {
            if let sel = selectedPoint { tooltip(for: sel) }
        }
    }

    private var yDomain: ClosedRange<Double> {
        var hi = points.reduce(0.0) { max($0, max($1.tip, $1.ambient)) }
        if let targetCelsius { hi = max(hi, targetCelsius) }
        let top = display(hi)
        // Anchor at 0 like the web chart, pad the top only.
        return 0...(top + max(2, top * 0.1))
    }

    private func tooltip(for point: ChartPoint) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(point.time.formatted(date: .omitted, time: .shortened))
                .foregroundStyle(Theme.text)
            Text("Internal \(Format.temp(point.tip, fahrenheit: usesFahrenheit))\(Format.unitSuffix(fahrenheit: usesFahrenheit))")
                .foregroundStyle(Theme.accent)
            Text("Ambient \(Format.temp(point.ambient, fahrenheit: usesFahrenheit))\(Format.unitSuffix(fahrenheit: usesFahrenheit))")
                .foregroundStyle(Theme.cool)
            if let targetCelsius, targetCelsius > 0 {
                Text("Target \(Format.temp(targetCelsius, fahrenheit: usesFahrenheit))\(Format.unitSuffix(fahrenheit: usesFahrenheit))")
                    .foregroundStyle(Theme.good)
            }
        }
        .font(.caption2.weight(.medium))
        .padding(9)
        .background(
            RoundedRectangle(cornerRadius: 8)
                .fill(Color(hex: 0x141A26).opacity(0.95))
                .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Color.white.opacity(0.12))))
        .padding(6)
        .allowsHitTesting(false)
    }
}

/// Legend row matching the web chart header.
struct ChartLegend: View {
    var body: some View {
        HStack(spacing: 14) {
            legendItem("Internal", Theme.accent)
            legendItem("Ambient", Theme.cool)
            legendItem("Target", Theme.good)
        }
    }

    private func legendItem(_ label: String, _ color: Color) -> some View {
        HStack(spacing: 6) {
            RoundedRectangle(cornerRadius: 2)
                .fill(color)
                .frame(width: 14, height: 3)
            Text(label)
                .font(.caption)
                .foregroundStyle(Theme.muted)
        }
    }
}
