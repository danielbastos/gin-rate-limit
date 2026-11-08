# Benchmarks iniciais de Memory

Execução local em 08/11/2026: Go 1.27.3, Linux amd64, Intel Core i7-12700H.
Comando: `go test ./tests -run '^$' -bench BenchmarkMemory -benchtime=20ms -benchmem`.

| Estratégia | Operação | ns/op | B/op | allocs/op |
| --- | --- | ---: | ---: | ---: |
| Event Log | Hit | 147,1 | 86 | 0 |
| Event Log | Get, 1 chave, 100 eventos/chave | 460,0 | 0 | 0 |
| Event Log | Get, 1 chave, 1000 eventos/chave | 2778 | 0 | 0 |
| Event Log | Get, 100 chaves, 100 eventos/chave | 462,1 | 0 | 0 |
| Event Log | Get, 100 chaves, 1000 eventos/chave | 2902 | 0 | 0 |
| Event Log | Clean | 4273 | 5760 | 32 |
| Event Log | CleanKey | 1714 | 720 | 4 |
| Buckets | Hit | 142,2 | 0 | 0 |
| Buckets | Get, 1 chave, 100 buckets/chave | 2066 | 0 | 0 |
| Buckets | Get, 1 chave, 1000 buckets/chave | 22588 | 0 | 0 |
| Buckets | Get, 100 chaves, 100 buckets/chave | 1959 | 0 | 0 |
| Buckets | Get, 100 chaves, 1000 buckets/chave | 21516 | 0 | 0 |
| Buckets | Clean | 14707 | 0 | 0 |
| Buckets | CleanKey | 4528 | 0 | 0 |

1. Amostra exploratória curta, sem SLA ou inferência de capacidade de produção.
2. `Hit` mede a API pública e renova o store a cada 4096 iterações fora da região
   cronometrada para limitar o histórico em memória.
3. `Get` mede diretamente o store para uma chave com janela de um minuto;
   Buckets usa resolução de um segundo e um registro por intervalo.
4. Limpeza usa oito chaves com 32 registros por chave, preservando os dez segundos
   finais e a fronteira do corte. A preparação ocorre fora da região cronometrada.
5. O armazenamento em memória prioriza simplicidade: consultas percorrem o histórico
   da chave. Não há pressuposição de que Buckets será mais rápido em toda carga.
6. Redis e PostgreSQL foram validados funcionalmente com serviços reais; estes
   números não medem os adapters remotos nem contenção entre processos.
