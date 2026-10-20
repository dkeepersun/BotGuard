<?php

declare(strict_types=1);

/**
 * What an attacker's script does: solve sha256-target-v1 outside any browser.
 * It exists to show honestly that PoW raises cost but does not identify bots.
 */
final class BotSolver
{
    /**
     * @param array{puzzles: int, target: string} $params
     * @return array{nonces: list<string>, attempts: int}
     */
    public static function solve(string $algorithm, string $challengeId, array $params): array
    {
        if ($algorithm !== 'sha256-target-v1') {
            throw new RuntimeException("unsupported algorithm $algorithm");
        }
        $target = hex2bin($params['target']);
        $nonces = [];
        $attempts = 0;
        for ($i = 0; $i < $params['puzzles']; $i++) {
            for ($n = 0; ; $n++) {
                $attempts++;
                $digest = hash('sha256', "BG1\n$algorithm\n$challengeId\n$i\n$n", true);
                // Binary strcmp compares unsigned bytes: big-endian digest < target.
                if (strcmp($digest, $target) < 0) {
                    $nonces[] = (string) $n;
                    break;
                }
            }
        }
        return ['nonces' => $nonces, 'attempts' => $attempts];
    }
}
