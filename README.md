# gin-rate-limit

Biblioteca Go para contar ocorrências em janelas móveis, com Event Log e Time
Buckets. O núcleo é independente de Gin; o pacote `gingatelimit` oferece um
middleware opcional. A aplicação define políticas e agendamento da limpeza.

Requer Go 1.26 ou superior.

## Uso em memória

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    ratelimit "github.com/danielbastos/gin-rate-limit"
    "github.com/danielbastos/gin-rate-limit/storage/memory"
)

func main() {
    ctx := context.Background()
    key, err := ratelimit.NewKey("login", "ip", "192.0.2.1", "email", "user@example.com")
    if err != nil { log.Fatal(err) }

    limiter, err := ratelimit.NewEventLog(memory.NewEventStore())
    if err != nil { log.Fatal(err) }
    if err := limiter.Hit(ctx, key); err != nil { log.Fatal(err) }
    count, err := limiter.Get(ctx, key, time.Minute)
    if err != nil { log.Fatal(err) }
    fmt.Println(count)

    // Remove apenas o histórico desta chave anterior ao corte.
    if err := limiter.CleanKey(ctx, key, time.Hour); err != nil { log.Fatal(err) }
}
```

Para usar buckets:

```go
limiter, err := ratelimit.NewBuckets(memory.NewBucketStore(), time.Second)
```

## Integração com Gin

```go
package main

import (
    "log"
    "net/http"
    "time"

    ratelimit "github.com/danielbastos/gin-rate-limit"
    "github.com/danielbastos/gin-rate-limit/gingatelimit"
    "github.com/danielbastos/gin-rate-limit/storage/memory"
    "github.com/gin-gonic/gin"
)

func main() {
    limiter, err := ratelimit.NewEventLog(memory.NewEventStore())
    if err != nil { log.Fatal(err) }

    middleware, err := gingatelimit.New(limiter, gingatelimit.Config{
        Limit: 60,
        Window: time.Minute,
        KeyFunc: func(c *gin.Context) (ratelimit.Key, error) {
            // FullPath usa o padrão registrado, como /users/:id.
            return ratelimit.NewKey("http:"+c.Request.Method+":"+c.FullPath(),
                "ip", c.ClientIP())
        },
    })
    if err != nil { log.Fatal(err) }

    router := gin.New()
    router.Use(gin.Recovery())
    // Sem proxy reverso. Atrás de um proxy, informe somente seus IPs/CIDRs.
    if err := router.SetTrustedProxies(nil); err != nil { log.Fatal(err) }
    router.POST("/login", middleware, func(c *gin.Context) {
        c.Status(http.StatusNoContent)
    })

    api := router.Group("/api")
    api.Use(middleware)
    api.GET("/users/:id", func(c *gin.Context) {
        c.Status(http.StatusNoContent)
    })

    log.Fatal(router.Run(":8080"))
}
```

O middleware aceita tanto `NewEventLog` quanto `NewBuckets`, com Memory, Redis
ou PostgreSQL. Memory compartilha contadores somente dentro do processo.
Instâncias que usam o mesmo espaço no Redis ou PostgreSQL compartilham o histórico,
mas a decisão de permitir continua não atômica.

| Campo de `Config` | Comportamento |
| --- | --- |
| `Limit` | Máximo de ocorrências admitidas por janela; `int64` positivo obrigatório |
| `Window` | Janela móvel positiva obrigatória |
| `KeyFunc` | Função obrigatória que retorna uma `ratelimit.Key` válida ou erro |
| `Skip` | Se retornar `true`, ignora geração de chave e armazenamento |
| `OnLimit` | Substitui a resposta JSON padrão `429` |
| `OnError` | Substitui a resposta padrão de erro; recebe o erro original encapsulado |
| `FailOpen` | Por padrão `false`; se `true`, permite continuar após falha de `Get` ou `Hit` |

O construtor retorna erros compatíveis com `errors.Is(err, gingatelimit.ErrInvalidConfig)`
para limiter nulo (inclusive ponteiro nulo em interface), limite ou janela não
positivos e `KeyFunc` ausente.

1. O fluxo é `Skip` → `KeyFunc` → `Get` → decisão → `Hit` → próximo handler.
2. Quando `count >= Limit`, responde `429 {"error":"rate limit exceeded"}` e
   interrompe a cadeia. Requisições bloqueadas não registram ocorrência.
3. O registro acontece antes do próximo handler e permanece mesmo se ele retornar
   erro. A primeira versão não oferece contagem apenas de respostas malsucedidas.
4. Erros de chave, incluindo uma `Key{}` sem erro, interrompem a cadeia com
   `500 {"error":"rate limiter key error"}`, inclusive com `FailOpen` habilitado.
5. Falhas de `Get` e `Hit` respondem `503 {"error":"rate limiter unavailable"}`
   por padrão. `FailOpen` permite continuar sem garantia de contagem. Uma falha de
   escrita remota pode ter resultado ambíguo; o middleware não repete a escrita.
6. Todas as operações recebem `c.Request.Context()`. Falhas quando esse contexto
   já está cancelado interrompem a cadeia mesmo com `FailOpen` habilitado.

Erros são anexados a `c.Errors`, preservando `errors.Is`, inclusive quando
`FailOpen` permite continuar; detalhes internos não aparecem nas respostas padrão.
`OnError` só executa quando a requisição é interrompida por erro. Antes de chamar
`OnLimit` ou `OnError`, o middleware já abortou a cadeia; o callback escreve a
resposta personalizada. Os callbacks podem executar simultaneamente em requisições
diferentes e devem proteger qualquer estado compartilhado.

**Concorrência:** `Get` e `Hit` são separados. Requisições simultâneas podem observar
o mesmo contador e ultrapassar o limite, inclusive entre várias instâncias usando
Redis ou PostgreSQL. Esta integração não fornece limite estrito. Time Buckets
também mantém sua contagem aproximada nas extremidades da janela.

Use namespaces diferentes para políticas independentes. O exemplo cria uma
política por método, padrão da rota e IP; um namespace fixo permite compartilhar
o limite entre rotas. Para limitar por usuário, registre a autenticação antes do
middleware e extraia a identidade autenticada em `KeyFunc`. O middleware não lê
o body nem cria identidades automaticamente. Ao usar `ClientIP`, configure os
[proxies confiáveis do Gin](https://gin-gonic.com/en/docs/server-config/trusted-proxies/)
para que headers enviados pelo cliente não alterem a identidade usada no limite.

Também é possível registrar globalmente com `router.Use(middleware)`. Nesse caso,
requisições sem rota podem ter `FullPath()` vazio; defina a política desejada em
`KeyFunc` ou `Skip`. Para ignorar health checks, por exemplo:

```go
Skip: func(c *gin.Context) bool { return c.Request.URL.Path == "/health" },
```

O middleware não inicia goroutines nem faz limpeza automática. Agende `Clean` na
aplicação: retenha pelo menos a maior janela consultada no mesmo espaço de dados
para Event Log e, para Buckets, essa janela **mais a resolução** para evitar que a
limpeza pelo início dos buckets remova ocorrências ainda necessárias. O exemplo
acima demonstra o registro HTTP; um serviço de longa duração precisa desse
agendamento. Não são emitidos headers de saldo, reset ou `Retry-After`, pois a API
atual não informa o instante exato em que haverá capacidade disponível.

## Contrato público

```go
type RateLimit interface {
    Get(ctx context.Context, key Key, window time.Duration) (int64, error)
    Hit(ctx context.Context, key Key) error
    Clean(ctx context.Context, window time.Duration) error
    CleanKey(ctx context.Context, key Key, window time.Duration) error
}
```

1. `Get` retorna a contagem disponível, sem gravar nem limpar registros.
2. `Hit` registra uma ocorrência, sem TTL.
3. `Clean` remove registros elegíveis de todas as chaves do store; em Buckets,
   atua apenas na resolução do limiter.
4. `CleanKey` faz a mesma limpeza para uma chave.

`Get` seguido de `Hit` não é uma autorização atômica: requisições concorrentes
podem observar o mesmo contador. A aplicação decide como permitir ou bloquear.

## Chaves e validação

`NewKey(namespace, components...)` aceita zero ou mais pares `nome, valor`.
Namespace, nomes e valores devem ser não vazios, e nomes não podem se repetir.
Os pares são ordenados por nome sem alterar o slice recebido. Não há normalização
automática, incluindo trim, lowercase, e-mail ou IP. `Key{}` é inválida.

`Key.String()` retorna `v1:` seguido do SHA-256 hexadecimal. O material do hash é:
prefixo `ratelimit:key:v1\x00`, namespace, quantidade de pares e pares ordenados.
Cada string é precedida pelo tamanho em bytes como uint64 big-endian; a quantidade
de pares também é uint64 big-endian. O vetor abaixo fixa a compatibilidade:

```text
NewKey("login", "email", "a@example.com", "ip", "192.0.2.1")
v1:19ca76a7475ec2988856cf9bac57c5fdbac0348708bea7fda4072df52c14b55c
```

Hash determinístico não anonimiza identificadores de baixa entropia. Os erros
da biblioteca não incluem os componentes originais.

Janelas devem ser positivas, com granularidade de `time.Duration` (nanossegundos).
Resoluções devem ser positivas e múltiplos inteiros de um segundo. Construtores
rejeitam stores nulos, inclusive ponteiros nulos encapsulados em interfaces.

Use `errors.Is` com `ErrInvalidKey`, `ErrInvalidWindow`, `ErrInvalidResolution` e
`ErrInvalidStore`. Falhas dos adapters e cancelamento preservam sua identidade.

## Semântica temporal e limpeza

| Regra | Event Log | Time Buckets |
| --- | --- | --- |
| Gravação | Um evento por ocorrência | Incremento no bucket `[start, start+resolution)` |
| Consulta | Eventos em `(now-window, now]` | Buckets com `start <= now` e `start+resolution > now-window` |
| Limpeza | Remove `at < now-window` | Remove `start < now-window` e `start < currentBucketStart` |
| Precisão da contagem | Exata sobre os eventos disponíveis | Aproximada nas extremidades |

Cada operação válida consulta `store.Now(ctx)` uma vez. Buckets são alinhados em
UTC por `now.UTC().Truncate(resolution)`, cuja origem é o instante zero de Go.
O bucket que abre exatamente em `now` participa da consulta. Resoluções distintas
não compartilham contadores nem limpeza.

Uma janela parcial inclui o contador inteiro de cada bucket intersectado.
Uma consulta concorrente também pode observar ocorrências posteriores ao horário
consultado dentro desse mesmo bucket. Buckets maiores reduzem a precisão.

A limpeza de buckets fechados usa o **início**, não o fim do bucket: pode remover
ocorrências ainda pertencentes à janela. O bucket aberto permanece preservado,
mesmo quando seu início é anterior ao corte. Esta é a regra de limpeza do contrato.

Não há retenção automática. Sem limpeza solicitada pela aplicação, o histórico
cresce. Uma limpeza com janela curta compromete consultas posteriores de janelas
maiores; elas retornam somente o histórico restante.

Cada incremento é atômico no adapter. Não há snapshot entre chamadas nem transação
global de limpeza. Uma escrita atrasada pode recriar histórico já removido;
limpezas canceladas podem ter removido parte dos registros. Retry de uma escrita
com resultado de rede ambíguo pode contar duas vezes.

## Redis

```go
import (
    "github.com/redis/go-redis/v9"
    redisstore "github.com/danielbastos/gin-rate-limit/storage/redis"
)

client := redis.NewClient(&redis.Options{
    Addr: "localhost:6379",
    ContextTimeoutEnabled: true,
})
defer client.Close() // a conexão pertence à aplicação

store, err := redisstore.NewEventStore(client, "my-service:ratelimit:v1")
if err != nil { log.Fatal(err) }
limiter, err := ratelimit.NewEventLog(store)
if err != nil { log.Fatal(err) }
```

Para buckets, use `redisstore.NewBucketStore(client, prefix)` e `NewBuckets`.
Os construtores não conectam nem verificam credenciais; erros de conexão aparecem
nas operações. Configure timeouts e cancelamento no client conforme sua aplicação.

1. Suporte inicial: Redis de instância única, usando `*redis.Client`.
2. O prefixo identifica o espaço da aplicação. Event Log, Buckets e resoluções
   recebem estruturas separadas; use prefixos distintos entre aplicações.
3. Eventos usam sorted sets lexicográficos com score zero e ID aleatório de
   128 bits por ocorrência. Eventos simultâneos não sobrescrevem um ao outro.
4. Timestamps são codificados por segundos Unix com sinal ordenável e nanossegundos,
   sem scores de ponto flutuante para representar tempo. Buckets mantêm contadores
   inteiros em hashes e índices lexicográficos.
5. Scripts mantêm dados e índices consistentes durante incremento e limpeza.
   Limpeza percorre os índices privados com `SSCAN` e remove lotes de até 512
   registros; não usa TTL, `KEYS` ou `FLUSHDB`.
6. A soma de buckets lê a série consultada em um script. Janelas extensas podem
   aumentar o tempo de execução no servidor; dimensione resolução e histórico.

## PostgreSQL

Use um schema dedicado e aplique explicitamente
[`storage/postgres/schema.sql`](storage/postgres/schema.sql) com a ferramenta de
migração da aplicação. A migração cria tabelas e índices no `search_path` escolhido;
os construtores não criam nem modificam o schema.

```go
import (
    "github.com/jackc/pgx/v5/pgxpool"
    postgresstore "github.com/danielbastos/gin-rate-limit/storage/postgres"
)

pool, err := pgxpool.New(ctx, dsn)
if err != nil { log.Fatal(err) }
defer pool.Close()
store, err := postgresstore.NewEventStore(pool, "my_service_ratelimit")
if err != nil { log.Fatal(err) }
limiter, err := ratelimit.NewEventLog(store)
if err != nil { log.Fatal(err) }
```

Para buckets, use `postgresstore.NewBucketStore(pool, schema)`. O nome do schema é
escapado como identificador SQL. Aplicações distintas devem usar schemas distintos.
A migração também está disponível como `postgresstore.SchemaSQL` para integração
com ferramentas próprias, sempre sob controle explícito do consumidor.

1. O relógio usa `clock_timestamp()` do servidor, com precisão de microssegundos.
2. O armazenamento usa nanossegundos Unix em `numeric(30,0)`: preserva timestamps
   e limites de consulta recebidos do Go sem arredondá-los para `timestamptz`.
3. Eventos têm identidade própria. Buckets usam upsert atômico sobre chave,
   resolução e início.
4. A limpeza executa statements em lotes de até 512 linhas, sem manter transação
   aberta entre todos os lotes.

A precisão do **relógio** de cada adapter difere da representação dos dados.
Memory usa `time.Now`; Redis usa `TIME`; PostgreSQL usa `clock_timestamp()`.
Os três respeitam os mesmos predicados para timestamps e limites recebidos.

## Validação local

```sh
go test ./...
go test -race ./...
go vet ./...
```

Os testes sem configuração remota executam Memory e ignoram explicitamente as
integrações. Para validar os três adapters com serviços reais:

```sh
docker compose -p gin-rate-limit up -d --wait
RATELIMIT_REQUIRE_INTEGRATION=1 \
RATELIMIT_REDIS_ADDR=127.0.0.1:16379 \
RATELIMIT_POSTGRES_DSN='postgres://ratelimit:local-test-only@127.0.0.1:15432/ratelimit?sslmode=disable' \
go test -race ./... -count=1
docker compose -p gin-rate-limit down -v
```

As credenciais do Compose são exclusivas para desenvolvimento local. As integrações
criam prefixos e schemas aleatórios e removem apenas seus próprios dados.
`RATELIMIT_REQUIRE_INTEGRATION=1` impede que a ausência de configuração seja tratada
como integração bem-sucedida. O CI executa os testes com Redis 8 e PostgreSQL 18.

O CI roda na abertura de PRs e na atualização de suas branches, cancelando
execuções anteriores da mesma PR. Os testes e `go vet` executam com Go 1.26.0
e a versão estável mais recente; as integrações remotas usam uma única versão,
definida em `go.mod`. Jobs adicionais verificam formatação, consistência de
`go.mod`/`go.sum` com `go mod tidy -diff`, análise estática com Staticcheck e
vulnerabilidades com govulncheck. As versões dessas duas ferramentas são fixadas
no workflow.

Benchmarks de Memory:

```sh
go test ./tests -run '^$' -bench BenchmarkMemory -benchmem
```

A suíte reutilizável em `tests/contract` permite validar outros adapters com os
mesmos cenários de eventos, buckets, estratégias públicas e middleware Gin.
`RunGinMiddleware` verifica o fluxo HTTP e o compartilhamento sequencial de
contadores entre routers, com isolamento por usuário e padrão de rota.

Os resultados locais iniciais e suas condições estão em
[`docs/benchmarks.md`](docs/benchmarks.md).

## Licença

Este projeto está disponível sob a [licença MIT](LICENSE).
