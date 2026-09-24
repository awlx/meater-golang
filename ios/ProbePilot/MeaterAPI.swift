import Foundation

/// Thin client for the Go server's HTTP API (internal/server/server.go).
struct MeaterAPI {
    var baseURL: URL

    enum APIError: LocalizedError {
        case badResponse(Int)
        case serverMessage(String)

        var errorDescription: String? {
            switch self {
            case .badResponse(let code): return "Server returned status \(code)"
            case .serverMessage(let msg): return msg
            }
        }
    }

    private var session: URLSession { .shared }

    // MARK: Reads

    func status() async throws -> ProbeStatus {
        try await get("api/status")
    }

    func history() async throws -> [HistoryPoint] {
        try await get("api/history")
    }

    func cooks() async throws -> [CookMeta] {
        try await get("api/cooks")
    }

    func cookDetail(id: Int64) async throws -> CookDetail {
        try await get("api/cooks/\(id)")
    }

    // MARK: Writes

    func setTarget(celsius: Double) async throws {
        try await post("api/target", body: ["celsius": celsius])
    }

    func startSession(name: String, meatType: String) async throws {
        try await post("api/session/start", body: ["name": name, "meatType": meatType])
    }

    func stopSession() async throws {
        try await post("api/session/stop", body: nil)
    }

    func setCookName(_ name: String) async throws {
        try await post("api/cook/name", body: ["name": name])
    }

    func setMeatType(_ meatType: String) async throws {
        try await post("api/cook/meat", body: ["meatType": meatType])
    }

    func deleteCook(id: Int64) async throws {
        var req = URLRequest(url: baseURL.appendingPathComponent("api/cooks/\(id)"))
        req.httpMethod = "DELETE"
        let (data, response) = try await session.data(for: req)
        try Self.check(response: response, data: data)
    }

    // MARK: SSE stream

    /// Opens /api/stream and yields one ProbeStatus per SSE event. The
    /// sequence ends when the connection drops; the caller reconnects.
    func statusStream() async throws -> AsyncThrowingStream<ProbeStatus, Error> {
        var req = URLRequest(url: baseURL.appendingPathComponent("api/stream"))
        req.timeoutInterval = 24 * 3600 // long-lived; keepalives every 15s
        req.setValue("text/event-stream", forHTTPHeaderField: "Accept")
        let (bytes, response) = try await session.bytes(for: req)
        try Self.check(response: response, data: nil)

        return AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    // The server sends each event as a single "data: {json}"
                    // line, so every data line is a complete frame; keepalive
                    // comment lines (": keepalive") are skipped.
                    for try await line in bytes.lines {
                        guard line.hasPrefix("data:") else { continue }
                        let payload = line.dropFirst(5).trimmingCharacters(in: .whitespaces)
                        guard let data = payload.data(using: .utf8),
                              let status = try? GoJSON.decoder.decode(ProbeStatus.self, from: data)
                        else { continue }
                        continuation.yield(status)
                    }
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    // MARK: Plumbing

    private func get<T: Decodable>(_ path: String) async throws -> T {
        let (data, response) = try await session.data(from: baseURL.appendingPathComponent(path))
        try Self.check(response: response, data: data)
        return try GoJSON.decoder.decode(T.self, from: data)
    }

    private func post(_ path: String, body: [String: Any]?) async throws {
        var req = URLRequest(url: baseURL.appendingPathComponent(path))
        req.httpMethod = "POST"
        if let body {
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            req.httpBody = try JSONSerialization.data(withJSONObject: body)
        }
        let (data, response) = try await session.data(for: req)
        try Self.check(response: response, data: data)
    }

    private static func check(response: URLResponse, data: Data?) throws {
        guard let http = response as? HTTPURLResponse else { return }
        guard (200..<300).contains(http.statusCode) else {
            if let data, let msg = String(data: data, encoding: .utf8),
               !msg.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                throw APIError.serverMessage(msg.trimmingCharacters(in: .whitespacesAndNewlines))
            }
            throw APIError.badResponse(http.statusCode)
        }
    }
}
