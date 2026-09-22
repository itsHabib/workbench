def plan(points):
    import random
    import time

    n = len(points)
    if n < 2:
        return list(range(n))

    deadline = time.perf_counter() + 0.8
    coords = list(points) + [[0, 0]]
    depot = n
    d = [
        [abs(x - u) + abs(y - v) for u, v in coords]
        for x, y in coords
    ]

    def cost(route):
        return sum(d[a][b] for a, b in zip(route, route[1:]))

    def improve(route, relocate=False):
        route = route[:]
        while time.perf_counter() < deadline:
            best_delta = 0
            move = None

            for i in range(1, n):
                a, b = route[i - 1], route[i]
                for j in range(i + 1, n + 1):
                    c, e = route[j], route[j + 1]
                    delta = d[a][c] + d[b][e] - d[a][b] - d[c][e]
                    if delta < best_delta:
                        best_delta = delta
                        move = (i, j)

            if move is not None:
                i, j = move
                route[i:j + 1] = reversed(route[i:j + 1])
                continue

            if not relocate:
                break

            block_move = None
            for length in range(1, min(3, n - 1) + 1):
                for i in range(1, n - length + 2):
                    end = i + length
                    a, b = route[i - 1], route[i]
                    c, e = route[end - 1], route[end]
                    removal = d[a][e] - d[a][b] - d[c][e]

                    for j in range(n + 1):
                        if i - 1 <= j < end:
                            continue
                        u, v = route[j], route[j + 1]
                        delta = removal + d[u][b] + d[c][v] - d[u][v]
                        reverse_delta = (
                            removal + d[u][c] + d[b][v] - d[u][v]
                        )
                        reverse = reverse_delta < delta
                        if reverse:
                            delta = reverse_delta
                        if delta < best_delta:
                            best_delta = delta
                            block_move = (i, end, j, reverse)

            if block_move is None:
                break

            i, end, j, reverse = block_move
            block = route[i:end]
            if reverse:
                block.reverse()
            del route[i:end]
            if j >= end:
                j -= end - i
            route[j + 1:j + 1] = block

        return route

    candidates = []
    best = [depot] + list(range(n)) + [depot]
    best_cost = cost(best)

    def consider(route):
        nonlocal best, best_cost
        value = cost(route)
        if value < best_cost:
            best, best_cost = route[:], value
        return value

    starts = sorted(range(n), key=lambda i: (d[depot][i], i))
    for start in starts:
        if time.perf_counter() >= deadline:
            break
        remaining = set(range(n))
        remaining.remove(start)
        route = [depot, start]
        while remaining:
            last = route[-1]
            nxt = min(remaining, key=lambda j: (d[last][j], d[depot][j], j))
            route.append(nxt)
            remaining.remove(nxt)
        route.append(depot)
        route = improve(route)
        candidates.append((consider(route), route))

    for farthest in (True, False):
        if time.perf_counter() >= deadline:
            break
        route = [depot, depot]
        remaining = set(range(n))
        proximity = d[depot][:n]
        while remaining:
            if farthest:
                vertex = max(remaining, key=lambda j: (proximity[j], -j))
            else:
                vertex = min(remaining, key=lambda j: (proximity[j], j))
            position = min(
                range(len(route) - 1),
                key=lambda k: (
                    d[route[k]][vertex] + d[vertex][route[k + 1]]
                    - d[route[k]][route[k + 1]]
                ),
            )
            route.insert(position + 1, vertex)
            remaining.remove(vertex)
            for j in remaining:
                proximity[j] = min(proximity[j], d[vertex][j])
        route = improve(route)
        candidates.append((consider(route), route))

    seen = set()
    for _, route in sorted(candidates, key=lambda item: item[0]):
        if time.perf_counter() >= deadline or len(seen) >= 8:
            break
        key = min(tuple(route[1:-1]), tuple(reversed(route[1:-1])))
        if key in seen:
            continue
        seen.add(key)
        consider(improve(route, relocate=True))

    rng = random.Random(0)
    for attempt in range(64):
        if time.perf_counter() >= deadline or best_cost == 0:
            break
        middle = best[1:-1]
        if n >= 8:
            a, b, c, e = sorted(rng.sample(range(n + 1), 4))
            middle = (
                middle[:a] + middle[c:e] + middle[b:c]
                + middle[a:b] + middle[e:]
            )
        else:
            a, b = sorted(rng.sample(range(n), 2))
            middle[a:b + 1] = reversed(middle[a:b + 1])
        route = improve([depot] + middle + [depot], relocate=attempt % 4 == 0)
        consider(route)

    return best[1:-1]
