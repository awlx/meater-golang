import Foundation
import Observation
import UserNotifications

/// Local notifications mirroring the web UI's alerts: ambient temperature out
/// of range and the one-shot "almost done" warning.
@MainActor
@Observable
final class NotificationManager {
    private(set) var authorized = false

    func requestAuthorization() async {
        let center = UNUserNotificationCenter.current()
        let settings = await center.notificationSettings()
        switch settings.authorizationStatus {
        case .notDetermined:
            authorized = (try? await center.requestAuthorization(options: [.alert, .sound, .badge])) ?? false
        case .authorized, .provisional, .ephemeral:
            authorized = true
        default:
            authorized = false
        }
    }

    func refreshAuthorization() async {
        let settings = await UNUserNotificationCenter.current().notificationSettings()
        authorized = settings.authorizationStatus == .authorized
            || settings.authorizationStatus == .provisional
    }

    func notify(title: String, body: String, id: String) {
        guard authorized else { return }
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        content.sound = .default
        content.interruptionLevel = .timeSensitive
        let request = UNNotificationRequest(identifier: id, content: content, trigger: nil)
        UNUserNotificationCenter.current().add(request)
    }
}
