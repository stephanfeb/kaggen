# Mobile Security Integration Guide

This guide covers secure integration of a Flutter mobile app with the Kaggen backend, focusing on QR code token onboarding, authenticated WebSocket connections, and security best practices.

## Overview

Kaggen uses token-based authentication for mobile clients. The onboarding flow:

1. Admin generates a token in the dashboard (with optional name and expiration)
2. Dashboard displays a QR code containing the WebSocket URL with the token
3. Mobile app scans the QR code and extracts the connection details
4. App stores the token securely and connects to the WebSocket endpoint
5. Server validates the token using Argon2-ID hash comparison

**Security highlights:**
- Tokens are hashed with Argon2-ID (64MB memory, 4 threads) — plaintext never stored on server
- Constant-time hash comparison prevents timing attacks
- Tokens support optional expiration
- Tokens can be revoked at any time via the dashboard or CLI

## QR Code Scanning & Token Extraction

### What the QR Code Contains

The QR code encodes a WebSocket URL in this format:

```
ws://192.168.1.100:18789/ws?token=N3x4ABCDef...
```

In production with TLS:

```
wss://kaggen.example.com/ws?token=N3x4ABCDef...
```

| Component | Description |
|-----------|-------------|
| Protocol | `ws://` (development) or `wss://` (production) |
| Host | Server IP or hostname |
| Port | Gateway port (default: 18789) |
| Path | `/ws` — the WebSocket endpoint |
| Token | Base64-URL encoded authentication token (~43 characters) |

### Scanning and Parsing

Use `mobile_scanner` or `qr_code_scanner` package to scan the QR code, then parse the URL:

```dart
import 'package:mobile_scanner/mobile_scanner.dart';

class QRScannerScreen extends StatefulWidget {
  @override
  State<QRScannerScreen> createState() => _QRScannerScreenState();
}

class _QRScannerScreenState extends State<QRScannerScreen> {
  final MobileScannerController _controller = MobileScannerController();

  void _onDetect(BarcodeCapture capture) {
    final barcode = capture.barcodes.firstOrNull;
    if (barcode == null || barcode.rawValue == null) return;

    final scannedUrl = barcode.rawValue!;

    // Validate and parse the URL
    final connectionInfo = _parseKaggenUrl(scannedUrl);
    if (connectionInfo != null) {
      _controller.stop();
      Navigator.pop(context, connectionInfo);
    }
  }

  KaggenConnection? _parseKaggenUrl(String url) {
    final uri = Uri.tryParse(url);
    if (uri == null) return null;

    // Validate scheme
    if (uri.scheme != 'ws' && uri.scheme != 'wss') {
      return null;
    }

    // Validate path
    if (uri.path != '/ws') {
      return null;
    }

    // Extract token
    final token = uri.queryParameters['token'];
    if (token == null || token.isEmpty) {
      return null;
    }

    return KaggenConnection(
      host: uri.host,
      port: uri.port,
      secure: uri.scheme == 'wss',
      token: token,
    );
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text('Scan Connection QR')),
      body: MobileScanner(
        controller: _controller,
        onDetect: _onDetect,
      ),
    );
  }
}
```

### Connection Data Model

```dart
class KaggenConnection {
  final String host;
  final int port;
  final bool secure;
  final String token;
  final DateTime? addedAt;
  final String? name;

  KaggenConnection({
    required this.host,
    required this.port,
    required this.secure,
    required this.token,
    this.addedAt,
    this.name,
  });

  String get wsUrl {
    final scheme = secure ? 'wss' : 'ws';
    return '$scheme://$host:$port/ws?token=$token';
  }

  Map<String, dynamic> toJson() => {
    'host': host,
    'port': port,
    'secure': secure,
    'token': token,
    'addedAt': addedAt?.toIso8601String(),
    'name': name,
  };

  factory KaggenConnection.fromJson(Map<String, dynamic> json) {
    return KaggenConnection(
      host: json['host'],
      port: json['port'],
      secure: json['secure'],
      token: json['token'],
      addedAt: json['addedAt'] != null
          ? DateTime.parse(json['addedAt'])
          : null,
      name: json['name'],
    );
  }
}
```

## Secure Token Storage

**CRITICAL**: Never store tokens in plain `SharedPreferences` or unencrypted files. Use platform-specific secure storage.

### Using flutter_secure_storage

Add the dependency:

```yaml
dependencies:
  flutter_secure_storage: ^9.0.0
```

Create a secure storage service:

```dart
import 'dart:convert';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

class SecureTokenStorage {
  static const _storage = FlutterSecureStorage(
    aOptions: AndroidOptions(
      encryptedSharedPreferences: true,
    ),
    iOptions: IOSOptions(
      accessibility: KeychainAccessibility.first_unlock_this_device,
    ),
  );

  static const _connectionKey = 'kaggen_connection';

  /// Save connection securely
  static Future<void> saveConnection(KaggenConnection connection) async {
    final json = jsonEncode(connection.toJson());
    await _storage.write(key: _connectionKey, value: json);
  }

  /// Load connection from secure storage
  static Future<KaggenConnection?> loadConnection() async {
    final json = await _storage.read(key: _connectionKey);
    if (json == null) return null;

    try {
      return KaggenConnection.fromJson(jsonDecode(json));
    } catch (e) {
      // Corrupted data — clear it
      await clearConnection();
      return null;
    }
  }

  /// Clear stored connection
  static Future<void> clearConnection() async {
    await _storage.delete(key: _connectionKey);
  }

  /// Check if a connection is stored
  static Future<bool> hasConnection() async {
    return await _storage.containsKey(key: _connectionKey);
  }
}
```

### Platform-Specific Notes

**iOS:**
- Tokens are stored in the iOS Keychain
- Use `KeychainAccessibility.first_unlock_this_device` to ensure the token is available after the first device unlock but not backed up to iCloud
- For higher security, use `KeychainAccessibility.when_unlocked` (requires device to be unlocked)

**Android:**
- Uses EncryptedSharedPreferences (AES-256-GCM encryption)
- Keys are stored in Android Keystore
- Set `encryptedSharedPreferences: true` for Android 6.0+

## WebSocket Connection Authentication

### Connecting with Token

The token can be passed in two ways:

**Option 1: Query Parameter (Recommended for WebSocket)**

```dart
import 'package:web_socket_channel/web_socket_channel.dart';

class KaggenClient {
  WebSocketChannel? _channel;
  final KaggenConnection connection;

  KaggenClient(this.connection);

  Future<void> connect() async {
    final uri = Uri.parse(connection.wsUrl);

    _channel = WebSocketChannel.connect(uri);

    // Wait for connection to establish
    await _channel!.ready;

    // Listen for the session assignment
    _channel!.stream.listen(
      _onMessage,
      onError: _onError,
      onDone: _onDone,
    );
  }

  void _onMessage(dynamic data) {
    final message = jsonDecode(data as String);

    if (message['type'] == 'session') {
      // Connection authenticated — store session ID
      final sessionId = message['session_id'];
      print('Connected with session: $sessionId');
    }

    // Handle other message types...
  }

  void _onError(Object error) {
    print('WebSocket error: $error');
    // Handle reconnection...
  }

  void _onDone() {
    print('WebSocket closed');
    // Handle reconnection...
  }

  void disconnect() {
    _channel?.sink.close();
  }
}
```

**Option 2: Authorization Header (for HTTP-based WebSocket libraries)**

```dart
final headers = {
  'Authorization': 'Bearer ${connection.token}',
};
```

### Handling Authentication Errors

The server returns HTTP status codes during the WebSocket upgrade:

| Status | Meaning | Action |
|--------|---------|--------|
| 101 | Switching Protocols | Success — connection established |
| 401 | Unauthorized | Token missing or invalid — prompt user to re-scan QR |
| 403 | Forbidden | Token revoked or expired — prompt user to re-scan QR |

```dart
Future<void> connect() async {
  try {
    final uri = Uri.parse(connection.wsUrl);
    _channel = WebSocketChannel.connect(uri);
    await _channel!.ready;
    // Connected successfully
  } on WebSocketChannelException catch (e) {
    if (e.message?.contains('401') == true) {
      throw TokenInvalidException('Token is invalid or missing');
    } else if (e.message?.contains('403') == true) {
      throw TokenRevokedException('Token has been revoked or expired');
    }
    rethrow;
  }
}

class TokenInvalidException implements Exception {
  final String message;
  TokenInvalidException(this.message);
}

class TokenRevokedException implements Exception {
  final String message;
  TokenRevokedException(this.message);
}
```

## Token Lifecycle Management

### Token Expiration

Tokens may have an expiration time set by the admin. The server validates expiration during connection and will reject expired tokens with a 403 status.

**Handling expiration:**

1. Catch the 403 error on connect
2. Clear the stored token
3. Prompt the user to scan a new QR code

```dart
Future<void> connectWithFallback() async {
  try {
    await connect();
  } on TokenRevokedException {
    await SecureTokenStorage.clearConnection();
    _showRescanPrompt();
  } on TokenInvalidException {
    await SecureTokenStorage.clearConnection();
    _showRescanPrompt();
  }
}
```

### Detecting Revoked Tokens

If a token is revoked while the app is connected, the server will close the WebSocket connection. Handle this in the `onDone` callback:

```dart
void _onDone() {
  // Check if this was an intentional disconnect
  if (!_intentionalDisconnect) {
    // Connection closed by server — token may be revoked
    _attemptReconnect();
  }
}

Future<void> _attemptReconnect() async {
  for (var attempt = 1; attempt <= 3; attempt++) {
    await Future.delayed(Duration(seconds: attempt * 2));

    try {
      await connect();
      return; // Success
    } on TokenRevokedException {
      // Token is definitely revoked
      await SecureTokenStorage.clearConnection();
      _showRescanPrompt();
      return;
    } catch (e) {
      // Transient error — try again
      continue;
    }
  }

  // All retries failed
  _showConnectionError();
}
```

### Graceful Reconnection

Implement exponential backoff for transient network failures:

```dart
class ReconnectionManager {
  int _attempt = 0;
  static const _maxAttempts = 5;
  static const _baseDelay = Duration(seconds: 1);
  static const _maxDelay = Duration(seconds: 30);

  Duration get nextDelay {
    final delay = _baseDelay * (1 << _attempt);
    return delay > _maxDelay ? _maxDelay : delay;
  }

  bool get shouldRetry => _attempt < _maxAttempts;

  void recordAttempt() => _attempt++;

  void reset() => _attempt = 0;
}
```

## Transport Security

### Using WSS in Production

**Always use `wss://` (WebSocket Secure) in production.** The server should be configured with TLS certificates.

```dart
// Validate that production connections use WSS
void validateConnection(KaggenConnection connection) {
  if (kReleaseMode && !connection.secure) {
    throw SecurityException(
      'Insecure WebSocket connections are not allowed in production',
    );
  }
}
```

### Certificate Pinning (Recommended)

For additional security, pin the server's TLS certificate to prevent MITM attacks:

```dart
import 'dart:io';

class PinnedHttpOverrides extends HttpOverrides {
  final List<String> pinnedCertificates; // SHA-256 fingerprints

  PinnedHttpOverrides(this.pinnedCertificates);

  @override
  HttpClient createHttpClient(SecurityContext? context) {
    final client = super.createHttpClient(context);

    client.badCertificateCallback = (cert, host, port) {
      // Calculate certificate fingerprint
      final fingerprint = _calculateFingerprint(cert);
      return pinnedCertificates.contains(fingerprint);
    };

    return client;
  }

  String _calculateFingerprint(X509Certificate cert) {
    // SHA-256 of DER-encoded certificate
    final digest = sha256.convert(cert.der);
    return digest.toString();
  }
}

// Apply globally at app startup
void main() {
  HttpOverrides.global = PinnedHttpOverrides([
    'abc123...', // Your server's certificate fingerprint
  ]);
  runApp(MyApp());
}
```

### Network Security Config (Android)

For Android, add a network security config to enforce TLS:

**android/app/src/main/res/xml/network_security_config.xml:**

```xml
<?xml version="1.0" encoding="utf-8"?>
<network-security-config>
    <!-- Allow cleartext for localhost only (development) -->
    <domain-config cleartextTrafficPermitted="true">
        <domain includeSubdomains="false">localhost</domain>
        <domain includeSubdomains="false">127.0.0.1</domain>
        <domain includeSubdomains="false">10.0.2.2</domain>
    </domain-config>

    <!-- Require TLS for all other domains -->
    <base-config cleartextTrafficPermitted="false">
        <trust-anchors>
            <certificates src="system" />
        </trust-anchors>
    </base-config>
</network-security-config>
```

**android/app/src/main/AndroidManifest.xml:**

```xml
<application
    android:networkSecurityConfig="@xml/network_security_config"
    ...>
```

### App Transport Security (iOS)

For iOS, configure ATS in **ios/Runner/Info.plist**:

```xml
<key>NSAppTransportSecurity</key>
<dict>
    <!-- Allow localhost for development -->
    <key>NSExceptionDomains</key>
    <dict>
        <key>localhost</key>
        <dict>
            <key>NSExceptionAllowsInsecureHTTPLoads</key>
            <true/>
        </dict>
    </dict>
</dict>
```

## Integration with Approval System

The approval system allows human-in-the-loop verification for sensitive agent actions. See **[Mobile Client Integration: Approval System](mobile_approval_integration.md)** for full details.

**Key security points:**

1. **Display approval details clearly** — Show the tool name, description, and arguments so users can make informed decisions
2. **Never auto-approve sensitive actions** — Let users explicitly approve each request
3. **Handle timeouts** — Approvals expire after 30 minutes
4. **Log decisions locally** — Keep a record of what was approved/rejected for user review

**Quick integration:**

```dart
void _onApprovalRequired(Map<String, dynamic> metadata) {
  final approval = ApprovalRequest(
    id: metadata['approval_id'],
    toolName: metadata['tool_name'],
    skillName: metadata['skill_name'],
    description: metadata['description'],
  );

  // Show approval dialog
  showDialog(
    context: context,
    barrierDismissible: false,
    builder: (ctx) => ApprovalDialog(
      approval: approval,
      onApprove: () => _submitApproval(approval.id, true),
      onReject: () => _submitApproval(approval.id, false),
    ),
  );
}

Future<void> _submitApproval(String id, bool approved) async {
  final endpoint = approved ? '/api/approvals/approve' : '/api/approvals/reject';
  await http.post(
    Uri.parse('$baseUrl$endpoint'),
    headers: {'Content-Type': 'application/json'},
    body: jsonEncode({'id': id}),
  );
}
```

## Security Best Practices Checklist

### Token Handling

- [ ] **Use secure storage** — Store tokens in Flutter Secure Storage (iOS Keychain / Android EncryptedSharedPreferences)
- [ ] **Never log tokens** — Exclude tokens from logs, crash reports, and analytics
- [ ] **Clear tokens on logout** — Delete stored tokens when user disconnects
- [ ] **Validate QR URLs** — Check scheme (`ws`/`wss`), path (`/ws`), and token presence before accepting

### Network Security

- [ ] **Use WSS in production** — Reject `ws://` connections in release builds
- [ ] **Consider certificate pinning** — Pin your server's TLS certificate for MITM protection
- [ ] **Configure platform security** — Set up Android Network Security Config and iOS ATS

### App Security

- [ ] **Obfuscate release builds** — Enable code obfuscation for release APKs/IPAs
- [ ] **No sensitive data in logs** — Sanitize all logging to exclude tokens, session IDs, and user data
- [ ] **Secure clipboard** — If allowing token copy, clear clipboard after short delay
- [ ] **Biometric unlock (optional)** — Require Face ID/fingerprint before accessing stored connections

### Connection Security

- [ ] **Handle auth errors gracefully** — Detect 401/403 and prompt for re-scan
- [ ] **Implement reconnection** — Use exponential backoff for transient failures
- [ ] **Validate server responses** — Check message types and structure before processing

## Error Handling & Edge Cases

| Scenario | Handling |
|----------|----------|
| Invalid QR code | Show error: "This QR code is not a valid Kaggen connection" |
| Token rejected (401) | Clear stored token, show: "Connection invalid. Please scan a new QR code." |
| Token expired (403) | Clear stored token, show: "Connection expired. Please scan a new QR code." |
| Network timeout | Retry with exponential backoff (max 5 attempts) |
| WebSocket closed unexpectedly | Attempt reconnection; if 403, treat as revoked |
| Server unreachable | Show offline indicator, retry when network returns |
| Certificate validation failed | Show: "Secure connection failed. Contact your administrator." |

## Testing & Verification

### Development Setup

For local development without TLS:

```dart
// Allow insecure connections in debug mode only
final connection = KaggenConnection(
  host: '192.168.1.100',
  port: 18789,
  secure: false, // Only in debug!
  token: 'test-token',
);

assert(!kReleaseMode || connection.secure,
    'Insecure connections not allowed in release');
```

### Mock Token for UI Development

Use this mock connection for UI testing without a live server:

```dart
final mockConnection = KaggenConnection(
  host: 'mock.kaggen.local',
  port: 18789,
  secure: true,
  token: 'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.mock',
  name: 'Test Connection',
  addedAt: DateTime.now(),
);
```

### Verifying Secure Storage

```dart
// Test that tokens are actually encrypted
Future<void> verifySecureStorage() async {
  // 1. Save a test token
  final testConnection = KaggenConnection(
    host: 'test',
    port: 18789,
    secure: true,
    token: 'secret-test-token-12345',
  );
  await SecureTokenStorage.saveConnection(testConnection);

  // 2. Verify it can be retrieved
  final loaded = await SecureTokenStorage.loadConnection();
  assert(loaded?.token == testConnection.token);

  // 3. Verify it's not in plain SharedPreferences
  final prefs = await SharedPreferences.getInstance();
  final allKeys = prefs.getKeys();
  for (final key in allKeys) {
    final value = prefs.getString(key);
    assert(value?.contains('secret-test-token') != true,
        'Token found in unencrypted storage!');
  }

  // 4. Clean up
  await SecureTokenStorage.clearConnection();
}
```

### Testing Reconnection Flow

```dart
void testReconnection() async {
  final client = KaggenClient(connection);

  // Connect
  await client.connect();
  assert(client.isConnected);

  // Simulate disconnect
  client.simulateDisconnect();

  // Wait for reconnection
  await Future.delayed(Duration(seconds: 5));
  assert(client.isConnected, 'Should have reconnected');
}
```

## API Reference

### Token Generation Endpoint

For reference, the dashboard generates tokens via:

```
POST /api/tokens/generate
Content-Type: application/json

{
  "name": "iPhone",
  "expires_in": "24h"  // Options: null (never), "24h", "7d", "30d", "90d"
}
```

**Response:**

```json
{
  "id": "kF9x2bE1V4Q",
  "token": "N3x4ABCDef...",
  "name": "iPhone",
  "ws_url": "ws://192.168.1.100:18789/ws?token=N3x4ABCDef...",
  "message": "Save this token now - it cannot be retrieved again!"
}
```

The `ws_url` is what gets encoded in the QR code.

### WebSocket Session Message

On successful connection, the server sends:

```json
{
  "type": "session",
  "session_id": "uuid-assigned-by-server"
}
```

Store this session ID for message routing and thread management.

---

## Related Documentation

- [Mobile Client Integration: Approval System](mobile_approval_integration.md) — Handling approval requests
- [Mobile Threading Guide](mobile-threading-guide.md) — Threaded conversations
- [Golang AI Bot Security Hardening Guide](Golang_AI_Bot_Security_Hardening_Guide.md) — Backend security implementation details
