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

    /**
     * @param array{timeout?: float, connect_timeout?: float, retries?: int, transport?: TransportInterface} $options
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
        $this->transport = $options['transport'] ?? new CurlTransport($options['timeout'] ?? 3.0, $options['connect_timeout'] ?? 1.0);
        $this->retries = max(0, (int) ($options['retries'] ?? 0));
    }

    public function verify(VerifyRequest $request): VerifyResult
    {
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

    private function attempt(VerifyRequest $request, string $body): VerifyResult
    {
        // Fresh timestamp and nonce per attempt; body and request_id unchanged.
        $headers = Signer::headers($this->secretId, $this->secretKey, $body);
        try {
            $resp = $this->transport->post($this->url, $headers, $body);
        } catch (TransportException $e) {
            throw new UnknownResultException('BotGuard 网络错误：' . $e->getMessage(), 0, null, $request->requestId, null, $e);
        }

        $json = json_decode($resp->body, true);
        $code = is_array($json) && is_string($json['code'] ?? null) ? $json['code'] : null;
        $message = is_array($json) && is_string($json['message'] ?? null) ? $json['message'] : 'HTTP ' . $resp->status;

        if ($resp->status === 200) {
            return $this->parseOk($request, $json);
        }
        if (in_array($resp->status, [400, 401, 403, 409], true)) {
            throw new CallerErrorException($message, $resp->status, $code, $request->requestId);
        }
        $retryAfter = isset($resp->headers['retry-after']) ? (int) $resp->headers['retry-after'] : null;
        throw new UnknownResultException($message, $resp->status, $code, $request->requestId, $retryAfter);
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
            $reasons);
    }
}
