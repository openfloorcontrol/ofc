(defn fib [n]
  (loop [a 0 b 1 i n]
    (if (zero? i)
      a
      (recur b (+ a b) (dec i)))))

(doseq [i (range 10)]
  (println (str "fib(" i ") = " (fib i))))

