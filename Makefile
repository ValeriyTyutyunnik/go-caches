.PHONY: test race bench baseline-bench bench-nonsharded bench-sharded-4 bench-sharded-8 bench-sharded-16 bench-sharded-32 mem-test clean

# Папка для сохранения сырых результатов
RESULTS_DIR := bench/results
TIMESTAMP := $(shell date +%Y-%m-%d_%H-%M-%S)

# Запуск юнит-тестов
test:
	go test -v ./...

# Запуск тестов с проверкой гонки
race:
	CGO_ENABLED=1 go test -race -v ./...

# Запуск всех бенчмарков на 5 секунд с сохранением в RESULTS_DIR
bench:
	go test -bench='Benchmark.*_ShardsScaling' -benchmem -benchtime=5s ./bench/... | tee $(RESULTS_DIR)/bench_$(TIMESTAMP).txt

baseline-bench:
	go test -bench='Benchmark.*_ShardsScaling' -benchmem -benchtime=10s ./bench/... | tee $(RESULTS_DIR)/baseline_bench.txt

# запуск бенча только для нешардированного кэша
bench-nonsharded:
	go test -bench='Benchmark.*_NonSharded' -benchmem -benchtime=5s ./bench/... | tee $(RESULTS_DIR)/bench_$(TIMESTAMP).txt

# запуск бенча для кэша с 4 шардами
bench-sharded-4:
	go test -bench='Benchmark.*_ShardsScaling/Sharded-4' -benchmem -benchtime=5s ./bench/... | tee $(RESULTS_DIR)/bench_$(TIMESTAMP).txt

# запуск бенча для кэша с 8 шардами
bench-sharded-8:
	go test -bench='Benchmark.*_ShardsScaling/Sharded-8' -benchmem -benchtime=5s ./bench/... | tee $(RESULTS_DIR)/bench_$(TIMESTAMP).txt

# запуск бенча для кэша с 16 шардами
bench-sharded-16:
	go test -bench='Benchmark.*_ShardsScaling/Sharded-16' -benchmem -benchtime=5s ./bench/... | tee $(RESULTS_DIR)/bench_$(TIMESTAMP).txt

# запуск бенча для кэша с 32 шардами
bench-sharded-32:
	go test -bench='Benchmark.*_ShardsScaling/Sharded-32' -benchmem -benchtime=5s ./bench/... | tee $(RESULTS_DIR)/bench_$(TIMESTAMP).txt


mem-test:
	go test -v -run TestMemoryOverhead -count=1 ./bench

# Очистка временных результатов
clean:
	rm -f $(RESULTS_DIR)/bench_*.txt $(RESULTS_DIR)/buckets_*.txt