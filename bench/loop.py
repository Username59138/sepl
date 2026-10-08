def main():
    total = 0
    for i in range(10000000):
        if i % 3 == 0:
            total += i
    print(total)
main()
