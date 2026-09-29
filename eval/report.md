# CodePilot RAG — eval report

| config | mode | Recall@1 | Recall@3 | Recall@5 | MRR |
|---|---|---|---|---|---|
| A | fts | 0.83 | 1.00 | 1.00 | 0.90 |
| B | vec | 0.75 | 0.92 | 1.00 | 0.85 |
| C | hybrid | 0.75 | 1.00 | 1.00 | 0.88 |
| D | hybrid+rerank | 0.75 | 1.00 | 1.00 | 0.86 |
| E | hybrid+blend | 0.75 | 1.00 | 1.00 | 0.86 |

## Срезы Recall@5

| config | по языку кода | по языку вопроса |
|---|---|---|
| A (fts) | go 1.00, js 1.00, python 1.00, vue 1.00 | en 1.00, ru 1.00 |
| B (vec) | go 1.00, js 1.00, python 1.00, vue 1.00 | en 1.00, ru 1.00 |
| C (hybrid) | go 1.00, js 1.00, python 1.00, vue 1.00 | en 1.00, ru 1.00 |
| D (hybrid+rerank) | go 1.00, js 1.00, python 1.00, vue 1.00 | en 1.00, ru 1.00 |
| E (hybrid+blend) | go 1.00, js 1.00, python 1.00, vue 1.00 | en 1.00, ru 1.00 |

## Провалы конфигурации D (hybrid+rerank)

Провалов нет: все вопросы найдены в топ-5.
