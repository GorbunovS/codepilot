<template>
  <div class="refund_flow">
    <h3>RefundFlow</h3>
    <p>Статус: {{ status }}</p>
  </div>
</template>

<script>
// RefundFlowPanel — панель «возврат»: оформляет возврат по заказу.
export default {
  name: "RefundFlowPanel",
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
    // refresh запускает обработку (возврат).
    async refresh() {
      this.processed = 0;
      this.skipped = 0;
    },
  },
};
</script>

<style scoped>
.refund_flow { padding: 0.5rem; }
</style>
