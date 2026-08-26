import Foundation
import Observation
import SwiftUI
import WidgetKit

/// A chart sample averaged into a 5-minute bucket, mirroring the web UI's
/// bucketing so long cooks stay light to draw.
struct ChartPoint: Identifiable, Equatable {
    var id: Int { bucket }
    var bucket: Int
    var time: Date       // bucket centre
    var tip: Double      // °C, running average
    var ambient: Double  // °C, running average

    fileprivate var count = 0
    fileprivate var sumTip = 0.0
    fileprivate var sumAmbient = 0.0
    fileprivate var lastSampleAt = Date.distantPast
}

/// Fixed 5-minute buckets, like the web chart.
private let bucketSeconds: TimeInterval = 5 * 60

enum ConnectionPhase: Equatable {
    case configuring   // no server URL yet
    case connecting
    case live
    case reconnecting
}

/// In-app ambient/ETA alert configuration, persisted like the web UI's
/// localStorage config. Temperatures are stored in Celsius.
struct AlertConfig: Codable, Equatable {
    var enabled = false
    var lowCelsius = 110.0
    var highCelsius = 125.0
    var etaEnabled = true
    var etaMinutes = 30
}

@MainActor
@Observable
final class AppModel {
    // MARK: Live state
    var status: ProbeStatus?
    var phase: ConnectionPhase = .configuring
    var series: [ChartPoint] = []
    var cooks: [CookMeta] = []

    /// When set, the chart shows this saved cook instead of the live series.
    var viewingCook: CookMeta?
    var viewingSeries: [ChartPoint] = []

    /// Active in-app alert banner, if any.
    enum BannerKind { case ambientLow, ambientHigh, almostDone }
    var banner: (kind: BannerKind, message: String)?

    var lastError: String?

    // MARK: Settings (persisted in the shared app group)
    var serverURLString: String {
        didSet {
            SharedStore.serverURLString = serverURLString
            restartStream()
        }
    }

    var usesFahrenheit: Bool {
        didSet {
            SharedStore.usesFahrenheit = usesFahrenheit
            pushToSurfaces(force: true)
        }
    }

    var liveActivityEnabled: Bool {
        didSet {
            SharedStore.defaults.set(liveActivityEnabled, forKey: "liveActivityEnabled")
            if let status { liveActivity.sync(status: status, usesFahrenheit: usesFahrenheit, enabled: liveActivityEnabled) }
        }
    }

    var alertConfig: AlertConfig {
        didSet {
            if let data = try? JSONEncoder().encode(alertConfig) {
                SharedStore.defaults.set(data, forKey: "alertConfig")
            }
            ambientAlertState = .ok
            if let status { evaluateAlerts(status) }
        }
    }

    // MARK: Internals
    @ObservationIgnored private var streamTask: Task<Void, Never>?
    @ObservationIgnored private var streamGeneration = 0
    @ObservationIgnored private let liveActivity = LiveActivityController()
    @ObservationIgnored let notifications = NotificationManager()
    @ObservationIgnored private var lastWidgetPush = Date.distantPast
    @ObservationIgnored private var lastWidgetSnapshot: WidgetSnapshot?

    private enum AmbientAlertState { case ok, low, high }
    @ObservationIgnored private var ambientAlertState: AmbientAlertState = .ok
    @ObservationIgnored private var etaWarned = false

    var api: MeaterAPI? {
        SharedStore.serverURL.map { MeaterAPI(baseURL: $0) }
    }

    init() {
        serverURLString = SharedStore.serverURLString
        usesFahrenheit = SharedStore.usesFahrenheit
        liveActivityEnabled = SharedStore.defaults.object(forKey: "liveActivityEnabled") as? Bool ?? true
        if let data = SharedStore.defaults.data(forKey: "alertConfig"),
           let cfg = try? JSONDecoder().decode(AlertConfig.self, from: data) {
            alertConfig = cfg
        } else {
            alertConfig = AlertConfig()
        }
    }

    // MARK: Lifecycle

    func onAppear() {
        Task { await notifications.refreshAuthorization() }
        restartStream()
    }

    func restartStream() {
        streamGeneration += 1
        let generation = streamGeneration
        streamTask?.cancel()
        series = []
        status = nil
        guard let api else {
            phase = .configuring
            streamTask = nil
            return
        }
        phase = .connecting
        streamTask = Task { [weak self] in
            await self?.runStream(api: api, generation: generation)
        }
        Task { await loadInitialData() }
    }

    private func runStream(api: MeaterAPI, generation: Int) async {
        var delay: Double = 1
        while !Task.isCancelled && generation == streamGeneration {
            do {
                let stream = try await api.statusStream()
                for try await status in stream {
                    guard generation == streamGeneration else { return }
                    delay = 1
                    phase = .live
                    apply(status)
                }
            } catch is CancellationError {
                return
            } catch {
                // fall through to reconnect
            }
            guard !Task.isCancelled && generation == streamGeneration else { return }
            phase = .reconnecting
            try? await Task.sleep(for: .seconds(delay))
            delay = min(delay * 2, 15)
        }
    }

    private func loadInitialData() async {
        guard let api else { return }
        if let points = try? await api.history() {
            series = Self.bucketize(points)
        }
        if let list = try? await api.cooks() {
            cooks = list
        }
    }

    // MARK: Applying a status frame

    private func apply(_ status: ProbeStatus) {
        let previousCookID = self.status?.cookId
        self.status = status
        if status.hasReading {
            if let previousCookID, previousCookID != status.cookId {
                series = [] // a new cook started; the chart follows it
            }
            appendSample(at: status.updatedAt, tip: status.tipCelsius, ambient: status.ambientCelsius)
            evaluateAlerts(status)
        }
        liveActivity.sync(status: status, usesFahrenheit: usesFahrenheit, enabled: liveActivityEnabled)
        pushToSurfaces(force: false)
    }

    private func appendSample(at time: Date, tip: Double, ambient: Double) {
        Self.fold(into: &series, time: time.isGoZeroTime ? Date() : time, tip: tip, ambient: ambient)
    }

    static func bucketize(_ points: [HistoryPoint]) -> [ChartPoint] {
        var out: [ChartPoint] = []
        for p in points {
            fold(into: &out, time: p.at, tip: p.tipCelsius, ambient: p.ambientCelsius)
        }
        return out
    }

    private static func fold(into series: inout [ChartPoint], time: Date, tip: Double, ambient: Double) {
        guard tip.isFinite else { return }
        let bucket = Int(time.timeIntervalSince1970 / bucketSeconds)
        if var last = series.last, bucket == last.bucket {
            guard time >= last.lastSampleAt else { return } // out of order
            last.count += 1
            last.sumTip += tip
            last.sumAmbient += ambient
            last.tip = last.sumTip / Double(last.count)
            last.ambient = last.sumAmbient / Double(last.count)
            last.lastSampleAt = time
            series[series.count - 1] = last
            return
        }
        if let last = series.last, bucket < last.bucket { return } // out of order
        var point = ChartPoint(
            bucket: bucket,
            time: Date(timeIntervalSince1970: (Double(bucket) + 0.5) * bucketSeconds),
            tip: tip,
            ambient: ambient)
        point.count = 1
        point.sumTip = tip
        point.sumAmbient = ambient
        point.lastSampleAt = time
        series.append(point)
    }

    // MARK: Widgets

    /// Writes the latest snapshot for the widgets and asks WidgetKit to
    /// redraw. Reloads are throttled; WidgetKit budgets them anyway.
    private func pushToSurfaces(force: Bool) {
        guard let status else { return }
        let snapshot = WidgetSnapshot(status: status, usesFahrenheit: usesFahrenheit)
        SharedStore.save(snapshot: snapshot)

        let visibleChange = lastWidgetSnapshot.map {
            $0.state != snapshot.state
                || abs($0.tipCelsius - snapshot.tipCelsius) >= 0.5
                || $0.running != snapshot.running
        } ?? true
        if force || visibleChange || Date().timeIntervalSince(lastWidgetPush) > 120 {
            lastWidgetPush = Date()
            lastWidgetSnapshot = snapshot
            WidgetCenter.shared.reloadAllTimelines()
        }
    }

    // MARK: Alerts

    private func evaluateAlerts(_ status: ProbeStatus) {
        // Ambient range alert with edge-triggered notifications, like the web.
        if alertConfig.enabled {
            let ambient = status.ambientCelsius
            let next: AmbientAlertState = ambient < alertConfig.lowCelsius ? .low
                : ambient > alertConfig.highCelsius ? .high : .ok
            if next != ambientAlertState {
                ambientAlertState = next
                switch next {
                case .ok:
                    if banner?.kind == .ambientLow || banner?.kind == .ambientHigh { banner = nil }
                case .low, .high:
                    let threshold = next == .low ? alertConfig.lowCelsius : alertConfig.highCelsius
                    let unit = Format.unitSuffix(fahrenheit: usesFahrenheit)
                    let message = "Ambient \(Format.tempWhole(ambient, fahrenheit: usesFahrenheit))\(unit.dropFirst()) is \(next == .low ? "below" : "above") \(Format.tempWhole(threshold, fahrenheit: usesFahrenheit))\(unit.dropFirst())"
                    banner = (next == .low ? .ambientLow : .ambientHigh, message)
                    notifications.notify(title: "Ambient alert", body: message, id: "meater-ambient")
                }
            }
        } else if banner?.kind == .ambientLow || banner?.kind == .ambientHigh {
            ambientAlertState = .ok
            banner = nil
        }

        // One-shot "almost done" alert.
        if alertConfig.etaEnabled, status.hasReading {
            let threshold = Double(alertConfig.etaMinutes) * 60
            if status.state == .cooking, status.etaSeconds > 0, status.etaSeconds <= threshold {
                if !etaWarned {
                    etaWarned = true
                    let mins = max(1, Int((status.etaSeconds / 60).rounded()))
                    let message = "Almost done — about \(mins) min to target"
                    banner = (.almostDone, message)
                    notifications.notify(title: "Cooking timer", body: message, id: "meater-eta")
                }
            } else if status.etaSeconds > threshold || status.state == .ready || status.state == .disconnected {
                etaWarned = false
            }
        }
    }

    func dismissBanner() { banner = nil }

    // MARK: Actions

    func setTarget(celsius: Double) {
        perform { try await $0.setTarget(celsius: celsius) }
    }

    func startSession(name: String, meatType: String) {
        series = []
        viewingCook = nil
        etaWarned = false
        perform {
            try await $0.startSession(name: name, meatType: meatType)
        } then: { [weak self] in
            await self?.reloadCooks()
        }
    }

    func stopSession() {
        perform { try await $0.stopSession() } then: { [weak self] in
            self?.liveActivity.endIfNeeded()
            await self?.reloadCooks()
        }
    }

    func saveCookInfo(name: String, meatType: String) {
        perform {
            try await $0.setCookName(name)
            try await $0.setMeatType(meatType)
        } then: { [weak self] in
            await self?.reloadCooks()
        }
    }

    func deleteCook(_ cook: CookMeta) {
        perform { try await $0.deleteCook(id: cook.id) } then: { [weak self] in
            guard let self else { return }
            if self.viewingCook?.id == cook.id { self.backToLive() }
            await self.reloadCooks()
        }
    }

    func viewCook(_ cook: CookMeta) {
        guard let api else { return }
        Task {
            do {
                let detail = try await api.cookDetail(id: cook.id)
                viewingSeries = Self.bucketize(detail.points)
                viewingCook = cook
            } catch {
                lastError = error.localizedDescription
            }
        }
    }

    func backToLive() {
        viewingCook = nil
        viewingSeries = []
    }

    func reloadCooks() async {
        guard let api else { return }
        if let list = try? await api.cooks() { cooks = list }
    }

    /// Distinct meat types across saved cooks, for the suggestion chips.
    var knownMeatTypes: [String] {
        var seen = Set<String>()
        var out: [String] = []
        for cook in cooks {
            let t = cook.meatType.trimmingCharacters(in: .whitespaces)
            if !t.isEmpty, seen.insert(t.lowercased()).inserted { out.append(t) }
        }
        return out.sorted { $0.localizedCaseInsensitiveCompare($1) == .orderedAscending }
    }

    private func perform(
        _ action: @escaping (MeaterAPI) async throws -> Void,
        then followUp: (@MainActor () async -> Void)? = nil
    ) {
        guard let api else {
            lastError = "Set the server address in Settings first."
            return
        }
        Task {
            do {
                try await action(api)
                lastError = nil
                await followUp?()
            } catch {
                lastError = error.localizedDescription
            }
        }
    }
}

// MARK: - Doneness presets (same table as the web UI)

struct DonenessPreset: Identifiable {
    let name: String
    let celsius: Double
    var id: String { name }
}

let donenessPresets: [DonenessPreset] = [
    .init(name: "Rare", celsius: 52),
    .init(name: "Med-Rare", celsius: 57),
    .init(name: "Medium", celsius: 63),
    .init(name: "Med-Well", celsius: 68),
    .init(name: "Well", celsius: 74),
    .init(name: "Poultry", celsius: 74),
    .init(name: "Pulled Pork", celsius: 95),
    .init(name: "Brisket", celsius: 96),
]
