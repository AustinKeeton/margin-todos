// MarginTodosSync: the Mac side of margin-todos. Receives the tablet's todos over HTTP and keeps
// one Apple Reminders list per notebook (synced to iPhone by iCloud).
//
// The tablet's detector POSTs to /sync whenever todos or check marks change, and every 2 minutes
// while it's awake. The reply carries check marks changed in Reminders since the last sync, so
// nothing ever connects to the tablet.
//
//   - New todo: its handwriting image is read with Vision (on-device) and becomes the reminder's
//     title, once. Edits made on the phone are never overwritten.
//   - Checked state syncs both ways. Whichever side changed since the last sync wins; if both did,
//     the more recent change wins.
//   - A box erased on the tablet deletes its reminder. A reminder deleted on the phone stays gone.
//   - Lists are named after notebooks and follow renames.
//
// Files: ~/Library/Application Support/MarginTodosSync/{config.json, state.json, images/}
// Open the app once on the Mac itself: it asks for Reminders access (macOS shows that prompt only
// in the desktop session, never over SSH).
import EventKit
import Foundation
import Network
import Vision

// MARK: - Storage

let supportDir = FileManager.default.homeDirectoryForCurrentUser
    .appendingPathComponent("Library/Application Support/MarginTodosSync")
let imagesDir = supportDir.appendingPathComponent("images")

struct Config: Codable {
    var token: String
    var port: UInt16
}

struct Item: Codable {
    var reminder: String // EKReminder.calendarItemIdentifier
    var synced: Bool     // checked state both sides agreed on at the last sync
}

struct SyncState: Codable {
    var lists: [String: String] = [:] // notebook id -> EKCalendar.calendarIdentifier
    var items: [String: Item] = [:]   // todo id -> reminder
    var dismissed: Set<String> = []   // todos whose reminder was deleted on the phone
}

// Logs to stderr and to log.txt (when opened as an app, stderr goes nowhere).
func log(_ message: String) {
    let line = "\(ISO8601DateFormatter().string(from: Date())) \(message)\n"
    FileHandle.standardError.write(Data(line.utf8))
    let path = supportDir.appendingPathComponent("log.txt")
    if let handle = try? FileHandle(forWritingTo: path) {
        handle.seekToEndOfFile()
        handle.write(Data(line.utf8))
        try? handle.close()
    } else {
        try? Data(line.utf8).write(to: path)
    }
}

func loadJSON<T: Decodable>(_ type: T.Type, _ url: URL) -> T? {
    guard let data = try? Data(contentsOf: url) else { return nil }
    return try? JSONDecoder().decode(type, from: data)
}

func saveJSON<T: Encodable>(_ value: T, _ url: URL) {
    let encoder = JSONEncoder()
    encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
    if let data = try? encoder.encode(value) {
        try? data.write(to: url, options: .atomic)
    }
}

// MARK: - Protocol

struct Todo: Codable {
    let id: String
    let doc: String
    let notebook: String
    let page: String
    let pageNumber: Int
    let checkedInInk: Bool
}

struct SyncRequest: Codable {
    let todos: [Todo]
    let state: [String: Bool]   // tablet check marks (taps); missing = checkedInInk
    let stateModified: Double?  // Unix ms when the tablet's check marks last changed
    let images: [String: String]? // todo id -> base64 PNG, for todos the Mac asked for
}

struct SyncResponse: Codable {
    var tabletUpdates: [String: Bool] = [:] // check marks changed in Reminders
    var needImages: [String] = []
}

// MARK: - OCR

func recognize(_ url: URL) -> String {
    let request = VNRecognizeTextRequest()
    request.recognitionLevel = .accurate
    request.usesLanguageCorrection = true
    request.recognitionLanguages = ["en-US"]
    try? VNImageRequestHandler(url: url, options: [:]).perform([request])
    let lines = (request.results ?? []).compactMap { $0.topCandidates(1).first?.string }
    return lines.joined(separator: " ").trimmingCharacters(in: .whitespaces)
}

// MARK: - Sync

final class Syncer {
    let store = EKEventStore()
    var state: SyncState
    let statePath = supportDir.appendingPathComponent("state.json")

    init() {
        state = loadJSON(SyncState.self, statePath) ?? SyncState()
    }

    func requestAccess() -> Bool {
        let done = DispatchSemaphore(value: 0)
        var granted = false
        store.requestFullAccessToReminders { ok, error in
            granted = ok
            if let error { log("Reminders access: \(error.localizedDescription)") }
            done.signal()
        }
        done.wait()
        return granted
    }

    var hasAccess: Bool { EKEventStore.authorizationStatus(for: .reminder) == .fullAccess }

    /// The notebook's list: the one recorded for it, else an existing list with its name,
    /// else a new one in the default account (iCloud). Renamed to follow the notebook.
    func list(for todo: Todo) throws -> EKCalendar {
        if let id = state.lists[todo.doc], let cal = store.calendar(withIdentifier: id) {
            if cal.title != todo.notebook {
                cal.title = todo.notebook
                try store.saveCalendar(cal, commit: true)
                log("renamed list to \(todo.notebook)")
            }
            return cal
        }
        if let existing = store.calendars(for: .reminder).first(where: { $0.title == todo.notebook }) {
            state.lists[todo.doc] = existing.calendarIdentifier
            log("using existing list \(todo.notebook)")
            return existing
        }
        let cal = EKCalendar(for: .reminder, eventStore: store)
        cal.title = todo.notebook
        guard let source = store.defaultCalendarForNewReminders()?.source else {
            throw NSError(domain: "MarginTodosSync", code: 1, userInfo: [NSLocalizedDescriptionKey: "no Reminders account"])
        }
        cal.source = source
        try store.saveCalendar(cal, commit: true)
        state.lists[todo.doc] = cal.calendarIdentifier
        log("created list \(todo.notebook)")
        return cal
    }

    func sync(_ req: SyncRequest) -> SyncResponse {
        var resp = SyncResponse()
        store.refreshSourcesIfNecessary()
        // Save any images the tablet sent.
        for (id, b64) in req.images ?? [:] {
            if let data = Data(base64Encoded: b64) {
                try? data.write(to: imagesDir.appendingPathComponent("\(id).png"), options: .atomic)
            }
        }
        let tabletTime = (req.stateModified ?? 0) / 1000
        let present = Set(req.todos.map(\.id))

        for todo in req.todos where !state.dismissed.contains(todo.id) {
            let tabletChecked = req.state[todo.id] ?? todo.checkedInInk
            guard let item = state.items[todo.id] else {
                // New todo: needs its image to read the handwriting.
                let image = imagesDir.appendingPathComponent("\(todo.id).png")
                guard FileManager.default.fileExists(atPath: image.path) else {
                    resp.needImages.append(todo.id)
                    continue
                }
                do {
                    let reminder = EKReminder(eventStore: store)
                    let text = recognize(image)
                    reminder.title = text.isEmpty ? "Todo on page \(todo.pageNumber)" : text
                    reminder.notes = "From reMarkable notebook \u{201C}\(todo.notebook)\u{201D}, page \(todo.pageNumber)."
                    reminder.calendar = try list(for: todo)
                    reminder.isCompleted = tabletChecked
                    try store.save(reminder, commit: true)
                    state.items[todo.id] = Item(reminder: reminder.calendarItemIdentifier, synced: tabletChecked)
                    log("added \u{201C}\(reminder.title ?? "")\u{201D} to \(todo.notebook)")
                } catch {
                    log("add \(todo.id): \(error.localizedDescription)")
                }
                continue
            }
            guard let reminder = store.calendarItem(withIdentifier: item.reminder) as? EKReminder else {
                // Deleted on the phone: leave it gone.
                state.items[todo.id] = nil
                state.dismissed.insert(todo.id)
                log("\(todo.id) was deleted in Reminders; won't recreate")
                continue
            }
            let phoneChecked = reminder.isCompleted
            let tabletChanged = tabletChecked != item.synced
            let phoneChanged = phoneChecked != item.synced
            var agreed = item.synced
            if tabletChanged && phoneChanged && tabletChecked != phoneChecked {
                let phoneTime = reminder.lastModifiedDate?.timeIntervalSince1970 ?? 0
                agreed = phoneTime > tabletTime ? phoneChecked : tabletChecked
            } else if tabletChanged {
                agreed = tabletChecked
            } else if phoneChanged {
                agreed = phoneChecked
            }
            if agreed != phoneChecked {
                reminder.isCompleted = agreed
                try? store.save(reminder, commit: true)
            }
            if agreed != tabletChecked {
                resp.tabletUpdates[todo.id] = agreed
            }
            state.items[todo.id]?.synced = agreed
        }

        // Boxes erased on the tablet (or notebooks deleted): remove their reminders.
        for (id, item) in state.items where !present.contains(id) {
            if let reminder = store.calendarItem(withIdentifier: item.reminder) as? EKReminder {
                try? store.remove(reminder, commit: true)
                log("removed \u{201C}\(reminder.title ?? "")\u{201D}: box erased on the tablet")
            }
            state.items[id] = nil
            try? FileManager.default.removeItem(at: imagesDir.appendingPathComponent("\(id).png"))
        }
        state.dismissed.formIntersection(present)
        saveJSON(state, statePath)
        return resp
    }
}

// MARK: - HTTP

final class Server {
    let config: Config
    let syncer: Syncer
    let queue = DispatchQueue(label: "sync") // one request at a time
    var listener: NWListener!

    init(config: Config, syncer: Syncer) {
        self.config = config
        self.syncer = syncer
    }

    func start() throws {
        listener = try NWListener(using: .tcp, on: NWEndpoint.Port(rawValue: config.port)!)
        listener.newConnectionHandler = { [weak self] conn in
            guard let self else { return }
            conn.start(queue: self.queue)
            self.receive(conn, Data())
        }
        listener.start(queue: queue)
        log("listening on :\(config.port)")
    }

    func receive(_ conn: NWConnection, _ buffer: Data) {
        conn.receive(minimumIncompleteLength: 1, maximumLength: 1 << 20) { [weak self] data, _, done, error in
            guard let self else { return }
            var buffer = buffer
            if let data { buffer.append(data) }
            if let (head, body) = Self.split(buffer), let length = Self.contentLength(head) {
                if body.count >= length {
                    self.handle(conn, head, body.prefix(length))
                    return
                }
            }
            if done || error != nil || buffer.count > 64 << 20 {
                conn.cancel()
                return
            }
            self.receive(conn, buffer)
        }
    }

    static func split(_ data: Data) -> (String, Data)? {
        guard let range = data.range(of: Data("\r\n\r\n".utf8)) else { return nil }
        let head = String(decoding: data[..<range.lowerBound], as: UTF8.self)
        return (head, data[range.upperBound...])
    }

    static func contentLength(_ head: String) -> Int? {
        for line in head.split(separator: "\r\n") {
            let parts = line.split(separator: ":", maxSplits: 1)
            if parts.count == 2, parts[0].lowercased() == "content-length" {
                return Int(parts[1].trimmingCharacters(in: .whitespaces))
            }
        }
        return head.hasPrefix("GET") ? 0 : nil
    }

    func handle(_ conn: NWConnection, _ head: String, _ body: Data) {
        let requestLine = head.split(separator: "\r\n").first.map(String.init) ?? ""
        let authorized = head.split(separator: "\r\n").contains {
            $0.lowercased().hasPrefix("authorization:") && $0.hasSuffix("Bearer \(config.token)")
        }
        guard authorized else { return respond(conn, 401, ["error": "unauthorized"]) }
        guard requestLine.hasPrefix("POST /sync ") else { return respond(conn, 404, ["error": "not found"]) }
        guard syncer.hasAccess else { return respond(conn, 503, ["error": "no Reminders access"]) }
        guard let req = try? JSONDecoder().decode(SyncRequest.self, from: body) else {
            return respond(conn, 400, ["error": "bad request"])
        }
        let resp = syncer.sync(req)
        let data = (try? JSONEncoder().encode(resp)) ?? Data("{}".utf8)
        send(conn, 200, data)
    }

    func respond(_ conn: NWConnection, _ status: Int, _ body: [String: String]) {
        send(conn, status, (try? JSONEncoder().encode(body)) ?? Data())
    }

    func send(_ conn: NWConnection, _ status: Int, _ body: Data) {
        let reason = [200: "OK", 400: "Bad Request", 401: "Unauthorized", 404: "Not Found", 503: "Service Unavailable"][status] ?? "Error"
        var out = Data("HTTP/1.1 \(status) \(reason)\r\nContent-Type: application/json\r\nContent-Length: \(body.count)\r\nConnection: close\r\n\r\n".utf8)
        out.append(body)
        conn.send(content: out, completion: .contentProcessed { _ in conn.cancel() })
    }
}

// MARK: - Main

try? FileManager.default.createDirectory(at: imagesDir, withIntermediateDirectories: true)
let configPath = supportDir.appendingPathComponent("config.json")
guard let config = loadJSON(Config.self, configPath) else {
    log("missing \(configPath.path)")
    exit(1)
}
let syncer = Syncer()

if CommandLine.arguments.contains("--request-access") {
    let ok = syncer.requestAccess()
    log(ok ? "Reminders access granted" : "Reminders access denied")
    let lists = syncer.store.calendars(for: .reminder).map(\.title).sorted()
    log("existing Reminders lists: \(lists.joined(separator: ", "))")
    exit(ok ? 0 : 1)
}

// Opened by hand for the first time: ask for Reminders access (shows the system prompt).
if EKEventStore.authorizationStatus(for: .reminder) == .notDetermined {
    log(syncer.requestAccess() ? "Reminders access granted" : "Reminders access denied")
    let lists = syncer.store.calendars(for: .reminder).map(\.title).sorted()
    log("existing Reminders lists: \(lists.joined(separator: ", "))")
}
if !syncer.hasAccess {
    log("no Reminders access: allow it in System Settings > Privacy & Security > Reminders")
}
let server = Server(config: config, syncer: syncer)
do { try server.start() } catch {
    log("listen: \(error)")
    exit(1)
}
dispatchMain()
