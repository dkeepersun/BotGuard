<?php

declare(strict_types=1);

namespace BotGuard;

use BotGuard\Exception\CallerErrorException;
use BotGuard\Exception\ProtocolException;
use BotGuard\Exception\UnknownResultException;
use BotGuard\Transport\CurlTransport;
use BotGuard\Transport\TransportException;
use BotGuard\Transport\TransportInterface;

/**
 * Server-side verify client, in the spirit of Yidun's NECaptchaVerifier: the
 * business server holds secret_id/secret_key, the browser only ever sees the
 * app_key and its token.
 *
 *   $client = new Client('https://botguard.example.com', $secretId, $secretKey);
 *   $result = $client->verify(new VerifyRequest($appKey, $_POST['botguard_token'] ?? '', 'login', VerifyRequest::newRequestId()));
 *   if (!$result->passed()) { ... $result->reason ... }
 *
 * Outcomes follow api/contract-v1.md: Passed and Rejected are returned;
 * CallerErrorException and UnknownResultException are thrown.
 */
final class Client
{
    private readonly string $url;
    private readonly TransportInterface $transport;
    private readonly int $retries;
    private readonly ?string $appKey;

    /** Local format checks, mirroring the gateway (internal/domain, internal/logic/gateway.go). */
    private const APP_KEY_PATTERN = '/\A[A-Za-z0-9_-]{8,64}\z/';
    private const ACTION_PATTERN = '/\A[a-z0-9_]{1,32}\z/';
    private const REQUEST_ID_PATTERN = '/\A[A-Za-z0-9_-]{1,64}\z/';
    /**
     * Tokens are opaque to callers, but every token the gateway issues is
     * base64url text joined by dots and at most 256 bytes; anything else is
     * TOKEN_INVALID on the gateway too, so it is rejected without a request.
     */
    private const TOKEN_PATTERN = '/\A[A-Za-z0-9._-]{1,256}\z/';

    /**
     * @param array{app_key?: string, timeout?: float|int|string, connect_timeout?: float|int|string, retries?: int, transport?: TransportInterface} $options
     *   app_key: default app for verifyToken().
     *   timeout / connect_timeout: seconds; empty, zero, negative or
     *   non-numeric values fall back to 3.0 / 1.0, tiny ones are raised to
     *   50 ms, so a request is never left without a time limit.
     *   retries: extra attempts after an unknown result (network error, 5xx),
     *   each re-signed with the same request_id. 429 is never retried here.
     */
    public function __construct(
        string $endpoint,
        private readonly string $secretId,
        #[\SensitiveParameter] private readonly string $secretKey,
        array $options = [],
    ) {
        if ($secretId === '' || $secretKey === '') {
            throw new \InvalidArgumentException('secret_id 和 secret_key 不能为空');
        }
        $this->url = rtrim($endpoint, '/') . Signer::VERIFY_PATH;
        $this->transport = $options['transport'] ?? new CurlTransport(
            CurlTransport::seconds($options['timeout'] ?? null, CurlTransport::DEFAULT_TIMEOUT),
            CurlTransport::seconds($options['connect_timeout'] ?? null, CurlTransport::DEFAULT_CONNECT_TIMEOUT),
        );
        $this->retries = max(0, (int) ($options['retries'] ?? 0));
        $this->appKey = isset($options['app_key']) && $options['app_key'] !== '' ? (string) $options['app_key'] : null;
    }

    /**
     * One-line form, like Yidun's verify($validate): uses the app_key given
     * at construction and a fresh request_id. Pass a request_id derived from
     * your own operation when you need to retry after an unknown result.
     *
     * $token is whatever the browser submitted: anything that is not a
     * well-formed token string (null, an array, bad UTF-8, oversized) is a
     * local TOKEN_INVALID rejection and no request is sent.
     */
    public function verifyToken(mixed $token, string $action, ?string $requestId = null): VerifyResult
    {
        if ($this->appKey === null) {
            throw new \LogicException('verifyToken 需要在构造 Client 时传入 app_key 选项');
        }
        return $this->verify(new VerifyRequest($this->appKey, is_string($token) ? $token : '', $action, $requestId ?? VerifyRequest::newRequestId()));
    }

    /**
     * Malformed app_key, action or request_id are fixed by the caller's code
     * or configuration, so they throw CallerErrorException (INVALID_REQUEST)
     * before anything is sent. A malformed token comes from the browser and
     * is a local Rejected result with reason TOKEN_INVALID, as the gateway
     * would answer.
     */
    public function verify(VerifyRequest $request): VerifyResult
    {
        $this->checkCallerFields($request);
        if (preg_match(self::TOKEN_PATTERN, $request->token) !== 1) {
            return VerifyResult::ofReject(VerifyResult::TOKEN_INVALID, $request->requestId);
        }
        $body = $request->toJson();
        for ($attempt = 0; ; $attempt++) {
            try {
                return $this->attempt($request, $body);
            } catch (UnknownResultException $e) {
                $retryable = !($e instanceof ProtocolException) && $e->httpStatus !== 429;
                if (!$retryable || $attempt >= $this->retries) {
                    throw $e;
                }
            }
        }
    }

    /** Keeps secret_key out of var_dump(), print_r() and debugger views. */
    public function __debugInfo(): array
    {
        return [
            'url' => $this->url,
            'secretId' => $this->secretId,
            'secretKey' => '[REDACTED]',
            'appKey' => $this->appKey,
            'retries' => $this->retries,
            'transport' => $this->transport::class,
        ];
    }

    private function checkCallerFields(VerifyRequest $request): void
    {
        $bad = match (true) {
            preg_match(self::APP_KEY_PATTERN, $request->appKey) !== 1 => 'app_key 格式错误：应为 [A-Za-z0-9_-]，8–64 个字符',
            preg_match(self::ACTION_PATTERN, $request->action) !== 1 => 'action 格式错误：应为 [a-z0-9_]，1–32 个字符，并由后端写死',
            preg_match(self::REQUEST_ID_PATTERN, $request->requestId) !== 1 => 'request_id 格式错误：应为 [A-Za-z0-9_-]，1–64 个字符',
            default => null,
        };
        if ($bad !== null) {
            // request_id is echoed back only when it is well-formed.
            $rid = preg_match(self::REQUEST_ID_PATTERN, $request->requestId) === 1 ? $request->requestId : null;
            throw new CallerErrorException('BotGuard 请求未发送，' . $bad, 0, 'INVALID_REQUEST', $rid);
        }
    }

    private function attempt(VerifyRequest $request, string $body): VerifyResult
    {
        // Fresh timestamp and nonce per attempt; body and request_id unchanged.
        $headers = Signer::headers($this->secretId, $this->secretKey, $body);
        try {
            $resp = $this->transport->post($this->url, $headers, $body);
        } catch (TransportException $e) {
            throw new UnknownResultException('BotGuard 网络错误：' . $e->getMessage(), 0, null, $request->requestId, null, $e);
        }
        $status = $resp->status;
        if ($resp->truncated) {
            // Retrying would only fetch the same oversized body again.
            throw new ProtocolException('BotGuard 响应体超过大小上限（HTTP ' . $status . '）', $status, null, $request->requestId);
        }

        $json = json_decode($resp->body, true);
        $code = is_array($json) && is_string($json['code'] ?? null) ? $json['code'] : null;
        $message = is_array($json) && is_string($json['message'] ?? null) ? $json['message'] : 'HTTP ' . $status;

        if ($status === 200) {
            return $this->parseOk($request, $json);
        }
        if ($status === 429 || ($status >= 500 && $status <= 599)) {
            $retryAfter = self::retryAfter($resp->headers['retry-after'] ?? null);
            throw new UnknownResultException($message, $status, $code, $request->requestId, $retryAfter);
        }
        if ($status >= 300 && $status <= 499) {
            // Redirects, 404/405 (wrong endpoint), 413/415 and the contract's
            // 400/401/403/409: retrying the same request cannot succeed.
            if ($code === null) {
                $message = 'BotGuard 返回 HTTP ' . $status . '，请检查 endpoint 与网关配置';
            }
            throw new CallerErrorException($message, $status, $code, $request->requestId);
        }
        throw new ProtocolException('BotGuard 返回了契约之外的 HTTP ' . $status, $status, $code, $request->requestId);
    }

    /**
     * Retry-After as delay-seconds or an HTTP-date (RFC 9110 §10.2.3);
     * null when absent or unparseable, 0 for a date in the past.
     */
    private static function retryAfter(?string $value): ?int
    {
        if ($value === null) {
            return null;
        }
        $value = trim((string) preg_replace('/\s+/', ' ', $value));
        if (preg_match('/\A\d+\z/', $value) === 1) {
            return (int) $value;
        }
        $utc = new \DateTimeZone('UTC');
        // IMF-fixdate, then the obsolete RFC 850 and asctime forms.
        foreach (['D, d M Y H:i:s \G\M\T', 'l, d-M-y H:i:s \G\M\T', 'D M j H:i:s Y'] as $format) {
            $at = \DateTimeImmutable::createFromFormat('!' . $format, $value, $utc);
            $errors = \DateTimeImmutable::getLastErrors();
            $clean = $errors === false || ($errors['warning_count'] === 0 && $errors['error_count'] === 0);
            if ($at !== false && $clean) {
                return max(0, $at->getTimestamp() - time());
            }
        }
        return null;
    }

    private function parseOk(VerifyRequest $request, mixed $json): VerifyResult
    {
        $data = is_array($json) && ($json['code'] ?? null) === 'OK' ? ($json['data'] ?? null) : null;
        if (!is_array($data) || !is_bool($data['success'] ?? null)) {
            throw new ProtocolException('BotGuard 响应格式不符合契约', 200, null, $request->requestId);
        }
        if ($data['success'] === false) {
            $reason = $data['reason'] ?? null;
            if (!is_string($reason) || $reason === '') {
                throw new ProtocolException('BotGuard 拒绝结果缺少 reason', 200, null, $request->requestId);
            }
            return VerifyResult::ofReject($reason, $request->requestId);
        }
        // A pass for a different app or action is not a pass for this request.
        if (($data['app_key'] ?? null) !== $request->appKey || ($data['action'] ?? null) !== $request->action
            || !is_int($data['verified_at'] ?? null)) {
            throw new ProtocolException('BotGuard 通过结果与请求的 app_key/action 不符', 200, null, $request->requestId);
        }
        // Risk fields are additive: absent on older gateways, never trusted to change the outcome.
        $reasons = array_values(array_filter((array) ($data['risk_reasons'] ?? []), 'is_string'));
        return VerifyResult::ofPass($data['app_key'], $data['action'], $data['verified_at'], $request->requestId,
            is_string($data['challenge_kind'] ?? null) ? $data['challenge_kind'] : null,
            is_string($data['risk_level'] ?? null) ? $data['risk_level'] : null,
            $reasons,
            is_string($data['origin'] ?? null) ? $data['origin'] : null);
    }
}
