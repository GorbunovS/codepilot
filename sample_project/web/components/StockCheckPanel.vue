<template>
  <div class="stock_check">
    <h3>StockCheck</h3>
    <p>Статус: {{ status }}</p>
  </div>
</template>

<script>
// StockCheckPanel — панель «остатки»: проверяет остатки товара.
export default {
  name: "StockCheckPanel",
  data() {
    return { processed: 0, skipped: 0 };
  },
  computed: {
    // status отображает итог обработки.
    status() {
      return `обработано: ${this.processed}, пропущено: ${this.skipped}`;
    },
  },
  methods: {
    // refresh запускает обработку (остатки).
    async refresh() {
      this.processed = 0;
      this.skipped = 0;
    },
  },
};
</script>

<style scoped>
.stock_check { padding: 0.5rem; }
</style>
