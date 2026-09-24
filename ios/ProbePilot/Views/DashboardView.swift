import SwiftUI

/// The main screen: a scrolling stack of cards mirroring the MEATER Monitor
/// web dashboard — ring, cook controls, stats, ETA, doneness presets, alerts,
/// chart, and past cooks.
struct DashboardView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.scenePhase) private var scenePhase

    @State private var showSettings = false

    var body: some View {
        NavigationStack {
            ZStack {
                Theme.background

                ScrollView {
                    VStack(spacing: 16) {
                        header
                        if let banner = model.banner { bannerView(banner) }
                        if model.phase == .configuring { configureCard }
                        ringCard
                        CookCard()
                        statsCard
                        EtaCard()
                        TargetsCard()
                        AlertsCard()
                        chartCard
                        CooksCard()
                        footer
                    }
                    .padding(.horizontal, 16)
                    .padding(.bottom, 28)
                }
            }
            .toolbar(.hidden, for: .navigationBar)
            .sheet(isPresented: $showSettings) {
                SettingsView()
                    .environment(model)
            }
        }
        .onAppear { model.onAppear() }
        .onChange(of: scenePhase) { _, phase in
            // Coming back to the foreground: the SSE task may have been
            // suspended for a long time; reconnect promptly.
            if phase == .active { model.restartStream() }
        }
    }

    // MARK: Header

    private var header: some View {
        HStack(spacing: 10) {
            Text("🥩")
                .font(.system(size: 27))
                .shadow(color: Theme.accent.opacity(0.35), radius: 6, y: 3)
            (Text("Probe").fontWeight(.heavy)
                + Text("Pilot").fontWeight(.semibold).foregroundColor(Theme.muted))
                .font(.system(size: 19))
                .foregroundStyle(Theme.text)
                .lineLimit(1)
                .minimumScaleFactor(0.6)

            Spacer(minLength: 4)

            connectionPill

            if let status = model.status, status.usingBridge, status.hasProbeRssi {
                StatusPill(text: "\(status.probeRssiDbm) dBm", tone: rssiTone(status.probeRssiDbm))
            }

            Button {
                model.usesFahrenheit.toggle()
            } label: {
                Text(model.usesFahrenheit ? "°F" : "°C")
                    .font(.subheadline.weight(.bold))
                    .foregroundStyle(Theme.text)
                    .frame(width: 44, height: 36)
                    .background(RoundedRectangle(cornerRadius: 12).fill(Theme.card))
                    .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(Theme.line, lineWidth: 1))
            }

            Button {
                showSettings = true
            } label: {
                Image(systemName: "gearshape.fill")
                    .font(.subheadline)
                    .foregroundStyle(Theme.muted)
                    .frame(width: 36, height: 36)
                    .background(RoundedRectangle(cornerRadius: 12).fill(Theme.card))
                    .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(Theme.line, lineWidth: 1))
            }
        }
        .padding(.top, 8)
    }

    private var connectionPill: some View {
        let (text, tone, pulses): (String, PillTone, Bool) = {
            guard model.phase != .configuring else { return ("no server", .off, false) }
            guard model.phase != .connecting else { return ("connecting…", .off, false) }
            guard model.phase != .reconnecting else { return ("reconnecting…", .off, false) }
            guard let s = model.status else { return ("connecting…", .off, false) }
            if !s.running { return ("stopped", .off, false) }
            if s.connected && s.hasReading { return ("live", .on, true) }
            if s.connected { return ("connected", .on, true) }
            if s.usingBridge && !s.bridgeConnected { return ("bridge offline", .off, false) }
            return ("searching…", .off, false)
        }()
        return StatusPill(text: text, tone: tone, pulses: pulses)
    }

    private func rssiTone(_ dbm: Int) -> PillTone {
        if dbm >= -70 { return .on }
        if dbm >= -90 { return .warn }
        return .off
    }

    // MARK: Banner

    private func bannerView(_ banner: (kind: AppModel.BannerKind, message: String)) -> some View {
        let isLow = banner.kind == .ambientLow
        let base = isLow ? Theme.cool : (banner.kind == .almostDone ? Theme.accent2 : Theme.bad)
        return HStack {
            Text(banner.message)
                .font(.subheadline.weight(.semibold))
            Spacer()
            Button("Dismiss") { model.dismissBanner() }
                .font(.footnote.weight(.bold))
                .padding(.horizontal, 12)
                .padding(.vertical, 6)
                .background(Capsule().fill(Color.white.opacity(0.12)))
                .overlay(Capsule().strokeBorder(Color.white.opacity(0.25)))
        }
        .foregroundStyle(Color.white.opacity(0.92))
        .padding(.horizontal, 16)
        .padding(.vertical, 13)
        .background(
            RoundedRectangle(cornerRadius: 14)
                .fill(LinearGradient(
                    colors: [base.opacity(0.28), base.opacity(0.14)],
                    startPoint: .top, endPoint: .bottom))
                .overlay(RoundedRectangle(cornerRadius: 14).strokeBorder(base.opacity(0.5))))
    }

    // MARK: Cards

    private var configureCard: some View {
        VStack(spacing: 10) {
            Text("Point the app at your meater-golang server to get cooking.")
                .font(.subheadline)
                .foregroundStyle(Theme.muted)
                .multilineTextAlignment(.center)
            Button("Set server address") { showSettings = true }
                .buttonStyle(AccentButtonStyle())
        }
        .frame(maxWidth: .infinity)
        .card()
    }

    private var ringCard: some View {
        VStack(spacing: 16) {
            RingView(status: model.status, usesFahrenheit: model.usesFahrenheit)
            StateBadge(state: model.status?.state ?? .waiting)
        }
        .frame(maxWidth: .infinity)
        .card()
    }

    private var statsCard: some View {
        VStack(spacing: 0) {
            statRow(label: "Ambient (cook)", value: ambientText, unit: "°")
            Divider().overlay(Theme.line)
            statRow(label: "Rise rate", value: rateText, unit: "°/min", small: true)
        }
        .card()
    }

    private var ambientText: String {
        guard let s = model.status, s.hasReading else { return "--" }
        return Format.temp(s.ambientCelsius, fahrenheit: model.usesFahrenheit)
    }

    private var rateText: String {
        guard let s = model.status, s.hasReading else { return "--" }
        return Format.rate(s.rateCelsiusPerMin, fahrenheit: model.usesFahrenheit)
    }

    private func statRow(label: String, value: String, unit: String, small: Bool = false) -> some View {
        HStack(alignment: .firstTextBaseline) {
            Text(label)
                .font(.subheadline)
                .foregroundStyle(Theme.muted)
            Spacer()
            HStack(alignment: .firstTextBaseline, spacing: 2) {
                Text(value)
                    .font(.system(size: small ? 22 : 28, weight: .heavy, design: .rounded))
                    .monospacedDigit()
                    .contentTransition(.numericText())
                    .animation(.snappy, value: value)
                    .foregroundStyle(Theme.text)
                Text(unit)
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(Theme.muted)
            }
        }
        .padding(.vertical, 8)
    }

    private var chartCard: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text(model.viewingCook?.displayName.uppercased() ?? "TEMPERATURE OVER TIME")
                    .font(.caption.weight(.semibold))
                    .kerning(1.2)
                    .foregroundStyle(Theme.muted)
                    .lineLimit(1)
                Spacer()
                if model.viewingCook != nil {
                    Button("← Live") { model.backToLive() }
                        .buttonStyle(SecondaryButtonStyle())
                }
            }
            ChartLegend()
            ChartView(
                points: model.viewingCook != nil ? model.viewingSeries : model.series,
                targetCelsius: model.viewingCook?.targetCelsius ?? model.status?.targetCelsius,
                usesFahrenheit: model.usesFahrenheit,
                emptyMessage: model.viewingCook != nil ? "No data for this cook" : "Collecting data…")
        }
        .card()
    }

    private var footer: some View {
        Group {
            if let s = model.status, s.hasReading, !s.updatedAt.isGoZeroTime {
                Text("updated \(s.updatedAt.formatted(date: .omitted, time: .standard))")
            } else {
                Text("waiting for probe…")
            }
        }
        .font(.caption)
        .foregroundStyle(Theme.muted)
        .padding(.top, 4)
    }
}
