def plan(points):
    import random
    import time

    started = time.perf_counter()
    deadline = started + 0.82
    groups = {}
    for index, point in enumerate(points):
        groups.setdefault((point[0], point[1]), []).append(index)

    origin = groups.pop((0, 0), [])
    coordinates = list(groups)
    n = len(coordinates)
    if n == 0:
        return origin[:]

    def expand(order):
        result = origin[:]
        for vertex in order:
            result.extend(groups[coordinates[vertex]])
        return result

    if n == 1:
        return expand([0])

    coordinates.append((0, 0))
    depot = n
    d = [
        [abs(x - u) + abs(y - v) for u, v in coordinates]
        for x, y in coordinates
    ]

    if n <= 10:
        size = 1 << n
        dp = [[float("inf")] * n for _ in range(size)]
        parent = [[-1] * n for _ in range(size)]
        for i in range(n):
            dp[1 << i][i] = d[depot][i]
        for mask in range(1, size):
            remaining = (size - 1) ^ mask
            ends = mask
            while ends:
                bit = ends & -ends
                i = bit.bit_length() - 1
                ends ^= bit
                value = dp[mask][i]
                todo = remaining
                while todo:
                    next_bit = todo & -todo
                    j = next_bit.bit_length() - 1
                    todo ^= next_bit
                    new_mask = mask | next_bit
                    candidate = value + d[i][j]
                    if candidate < dp[new_mask][j]:
                        dp[new_mask][j] = candidate
                        parent[new_mask][j] = i
        mask = size - 1
        last = min(range(n), key=lambda i: dp[mask][i] + d[i][depot])
        order = []
        while last != -1:
            order.append(last)
            previous = parent[mask][last]
            mask ^= 1 << last
            last = previous
        order.reverse()
        return expand(order)

    rng = random.Random(0)

    def cost(route):
        return sum(d[route[i]][route[i + 1]] for i in range(n + 1))

    def improve(route, thorough=True):
        route = route[:]
        while time.perf_counter() < deadline:
            gain = 0
            move = None
            for i in range(1, n):
                a, b = route[i - 1], route[i]
                da, db = d[a], d[b]
                old = da[b]
                for j in range(i + 1, n + 1):
                    c, e = route[j], route[j + 1]
                    delta = da[c] + db[e] - old - d[c][e]
                    if delta < gain:
                        gain = delta
                        move = (i, j)
            if move is not None:
                i, j = move
                route[i:j + 1] = route[i:j + 1][::-1]
                continue
            if not thorough or time.perf_counter() >= deadline:
                break

            for length in (1, 2, 3):
                if time.perf_counter() >= deadline:
                    return route
                for i in range(1, n - length + 2):
                    end = i + length
                    a, b = route[i - 1], route[i]
                    c, e = route[end - 1], route[end]
                    removal = d[a][e] - d[a][b] - d[c][e]
                    db, dc = d[b], d[c]
                    for j in range(n + 1):
                        if i - 1 <= j < end:
                            continue
                        u, v = route[j], route[j + 1]
                        base = removal - d[u][v]
                        delta = base + db[u] + dc[v]
                        if delta < gain:
                            gain = delta
                            move = (i, end, j, False)
                        if length > 1:
                            delta = base + dc[u] + db[v]
                            if delta < gain:
                                gain = delta
                                move = (i, end, j, True)
            if move is not None:
                i, end, j, reverse = move
                block = route[i:end]
                if reverse:
                    block.reverse()
                del route[i:end]
                if j >= end:
                    j -= end - i
                route[j + 1:j + 1] = block
                continue

            if time.perf_counter() >= deadline:
                break
            swap = None
            for i in range(1, n - 1):
                a, b, c = route[i - 1:i + 2]
                db = d[b]
                removal = -d[a][b] - db[c]
                for j in range(i + 2, n + 1):
                    u, v, w = route[j - 1:j + 2]
                    delta = (
                        removal - d[u][v] - d[v][w]
                        + d[a][v] + d[v][c] + db[u] + db[w]
                    )
                    if delta < gain:
                        gain = delta
                        swap = (i, j)
            if swap is None:
                break
            i, j = swap
            route[i], route[j] = route[j], route[i]
        return route

    def insert_vertices(route, remaining, regret=False, noise=0):
        route = route[:]
        remaining = list(remaining)
        while remaining:
            choices = []
            for vertex in remaining:
                dv = d[vertex]
                first = float("inf")
                second = float("inf")
                position = 0
                for k in range(len(route) - 1):
                    a, b = route[k], route[k + 1]
                    delta = dv[a] + dv[b] - d[a][b]
                    if delta < first:
                        second, first = first, delta
                        position = k + 1
                    elif delta < second:
                        second = delta
                if second == float("inf"):
                    second = first
                priority = first
                if regret:
                    priority = first - 2 * (second - first)
                if noise:
                    priority += rng.uniform(-noise, noise)
                choices.append((priority, first, vertex, position))
            _, _, vertex, position = min(choices)
            route.insert(position, vertex)
            remaining.remove(vertex)
        return route

    xs = [p[0] for p in coordinates]
    ys = [p[1] for p in coordinates]
    lower_bound = 2 * (max(xs) - min(xs) + max(ys) - min(ys))
    best = [depot] + list(range(n)) + [depot]
    best_cost = cost(best)
    seeds = []
    elite = []
    elite_keys = set()

    def remember(route):
        nonlocal best, best_cost
        value = cost(route)
        if value < best_cost:
            best, best_cost = route[:], value
        middle = tuple(route[1:-1])
        key = min(middle, middle[::-1])
        if key not in elite_keys:
            elite.append((value, route[:], key))
            elite_keys.add(key)
            elite.sort(key=lambda item: item[0])
            if len(elite) > 10:
                removed = elite.pop()
                elite_keys.remove(removed[2])
        return value

    for mode in range(3):
        if time.perf_counter() >= deadline:
            break
        if mode == 2:
            route = insert_vertices([depot, depot], range(n), regret=True)
        else:
            route = [depot, depot]
            remaining = set(range(n))
            proximity = d[depot][:n]
            while remaining:
                vertex = max(remaining, key=lambda j: (proximity[j], -j))
                dv = d[vertex]
                position = min(
                    range(len(route) - 1),
                    key=lambda k: dv[route[k]] + dv[route[k + 1]]
                    - d[route[k]][route[k + 1]],
                )
                route.insert(position + 1, vertex)
                remaining.remove(vertex)
                for j in remaining:
                    if mode == 0:
                        proximity[j] = min(proximity[j], dv[j])
                    else:
                        proximity[j] += dv[j]
        route = improve(route)
        seeds.append((remember(route), route))
        if best_cost == lower_bound:
            return expand(best[1:-1])

    starts = sorted(range(n), key=lambda i: (d[depot][i], i))
    for start in starts:
        if time.perf_counter() - started >= 0.20:
            break
        remaining = set(range(n))
        remaining.remove(start)
        route = [depot, start]
        while remaining:
            last = route[-1]
            vertex = min(remaining, key=lambda j: (d[last][j], d[depot][j], j))
            route.append(vertex)
            remaining.remove(vertex)
        route.append(depot)
        route = improve(route, False)
        seeds.append((remember(route), route))

    refined = set()
    for _, route in sorted(seeds, key=lambda item: item[0]):
        if time.perf_counter() - started >= 0.32 or len(refined) >= 6:
            break
        middle = tuple(route[1:-1])
        key = min(middle, middle[::-1])
        if key in refined:
            continue
        refined.add(key)
        remember(improve(route))
        if best_cost == lower_bound:
            return expand(best[1:-1])

    current = best[:]
    current_cost = best_cost
    stale = 0
    attempt = 0
    while time.perf_counter() < deadline and best_cost > lower_bound:
        attempt += 1
        previous_best = best_cost
        if stale >= 12:
            current = elite[rng.randrange(min(5, len(elite)))][1][:]
            current_cost = cost(current)
            stale = 0

        if attempt % 3:
            route = current
            count = min(n - 2, rng.randint(3, 8 + min(stale // 3, 3)))
            if attempt % 4 == 0:
                center = rng.randrange(n)
                vertices = sorted(
                    range(n),
                    key=lambda j: d[center][j] * rng.uniform(0.7, 1.3),
                )[:count]
            elif attempt % 4 == 1:
                begin = rng.randrange(n)
                middle = route[1:-1]
                vertices = [middle[(begin + k) % n] for k in range(count)]
            else:
                vertices = rng.sample(range(n), count)
            removed = set(vertices)
            partial = [v for v in route if v not in removed]
            noise = best_cost / n * (0.12 if attempt % 5 == 0 else 0)
            route = insert_vertices(
                partial, vertices, regret=attempt % 2 == 0, noise=noise
            )
        else:
            middle = current[1:-1]
            a, b, c, e = sorted(rng.sample(range(n + 1), 4))
            middle = (
                middle[:a] + middle[c:e] + middle[b:c]
                + middle[a:b] + middle[e:]
            )
            if attempt % 9 == 0:
                a, b = sorted(rng.sample(range(n), 2))
                middle[a:b + 1] = middle[a:b + 1][::-1]
            route = [depot] + middle + [depot]

        route = improve(route)
        value = remember(route)
        stale = 0 if best_cost < previous_best else stale + 1
        if value <= current_cost:
            current, current_cost = route, value
        elif rng.random() < 0.12 and value <= best_cost * 1.035:
            current, current_cost = route, value
        elif attempt % 7 == 0:
            current, current_cost = best[:], best_cost

    return expand(best[1:-1])
