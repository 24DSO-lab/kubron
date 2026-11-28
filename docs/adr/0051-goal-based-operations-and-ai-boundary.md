# ADR-0051: Goal-based Operations и границы AI/Intelligence

- Статус: предложено
- Дата: 2026-11-28
- Связано: ADR-0024, ADR-0025, ADR-0031…0034, ADR-0039, ADR-0042…0047; Kubron Platform Architecture Roadmap

## Контекст

Долгосрочная цель Kubron — позволить оператору описать желаемый результат, а платформе построить и выполнить последовательность безопасных операций. При этом Intelligence и AI могут ошибаться, а произвольная генерация команд не совместима с typed actions, authorization и audit guarantees.

## Решение

1. Goal-based Operation принимает декларативную цель, scope, ограничения, бюджет и критерии успеха. Цель не является командой и сама по себе не даёт прав на изменение Kubernetes.
2. Planner преобразует Goal только в конечный набор известных, версионируемых Operations/Actions из каталога ADR-0043. Неподдерживаемая цель завершается объяснимым отказом или read-only рекомендацией, а не генерацией произвольного executable payload.
3. Планирование ограничено явными budgets: число targets/actions/steps, глубина decomposition, время, стоимость/ресурсы и blast radius. Все лимиты фиксируются в Plan.
4. Каждый сгенерированный план проходит обычные Target resolution, authorization, policy, risk, Plan Commit и Approval gates. Goal-based path не имеет привилегированного bypass.
5. AI/Intelligence может:
   - собирать и суммировать evidence;
   - формировать hypotheses и confidence;
   - предлагать цель, typed actions и объяснение;
   - помогать сравнивать варианты и прогнозировать последствия.
6. AI/Intelligence не может:
   - создавать или расширять grants/delegations;
   - менять tenant ownership, policy или risk classification;
   - самостоятельно подтверждать approval;
   - выполнять произвольный shell/kubectl/code;
   - считать собственную hypothesis доказанной причиной без подтверждающего evidence;
   - обходить backend authorization, critical audit, verification или recovery.
7. Goal completion определяется только проверенными postconditions. Формулировка пользователя или ответ модели не является доказательством достижения цели.
8. Goal-based workflows внедряются после зрелости typed action catalog, Plan/Approval, Durable Run, verification/recovery и policy enforcement; сначала read-only/Assisted, затем ограниченные Autonomous сценарии.

## Альтернативы

- AI agent с прямым доступом к Kubernetes API.
- Исполнение команд, сгенерированных LLM.
- Считать ответ модели доказательством результата.
- Давать Goal отдельный privileged execution path.

Отклонены из-за отсутствия воспроизводимых security boundaries и невозможности гарантировать проверяемость side effects.

## Последствия

- Goal planner остаётся над typed Operations, а не заменяет их.
- Все generated plans детерминированно валидируются обычными доменами.
- Нужны отдельные tests на prompt injection/untrusted evidence, scope escape, budget exhaustion и unsupported goals.
- ADR не разрешает Autonomous execution и не означает наличие Goal runtime.
