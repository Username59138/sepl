def main():
    items = []
    for i in range(1000000):
        items.append(i * 2)
    total = 0
    for x in items:
        total += x
    words = {}
    for i in range(200000):
        k = str(i % 1000)
        words[k] = words.get(k, 0) + 1
    print(total, len(words))
main()
